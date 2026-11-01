// Package relaytrace records bounded transport facts, without request content,
// credentials, or any inference that a provider charged for a request.
package relaytrace

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

const EdgeRequestIDHeader = "X-Request-Id"

const maxEntries = 32

func Enabled() bool { return os.Getenv("RELAY_AUDIT_ENABLED") != "false" }

type contextKey struct{}

type Observation struct {
	At                 time.Time `json:"at"`
	Stage              string    `json:"stage"`
	ErrorClass         string    `json:"error_class,omitempty"`
	ErrorType          string    `json:"error_type,omitempty"`
	WebSocketCloseCode int       `json:"websocket_close_code,omitempty"`
}

type AttemptSnapshot struct {
	Index               int               `json:"index"`
	ChannelID           int               `json:"channel_id"`
	Method              string            `json:"method"`
	Origin              string            `json:"origin"`
	Protocol            string            `json:"protocol"`
	StartedAt           time.Time         `json:"started_at"`
	HeadersAt           *time.Time        `json:"headers_at,omitempty"`
	EndedAt             *time.Time        `json:"ended_at,omitempty"`
	Status              int               `json:"status,omitempty"`
	NegotiatedProtocol  string            `json:"negotiated_protocol,omitempty"`
	RequestIDs          map[string]string `json:"request_ids,omitempty"`
	PrimaryID           string            `json:"primary_request_id,omitempty"`
	ResponseObjectID    string            `json:"response_object_id,omitempty"`
	TerminalEvent       string            `json:"terminal_event,omitempty"`
	UsageObjectObserved bool              `json:"usage_object_observed,omitempty"`
	RequestBytesRead    int64             `json:"request_body_bytes_read"`
	ResponseBytesRead   int64             `json:"response_body_bytes_read"`
	BodyEOF             bool              `json:"body_eof"`
	BodyClosed          bool              `json:"body_closed"`
	ConnectionReused    bool              `json:"connection_reused"`
	TransportWrites     int               `json:"transport_writes"`
	DNSMS               *int64            `json:"dns_ms,omitempty"`
	ConnectMS           *int64            `json:"connect_ms,omitempty"`
	TLSMS               *int64            `json:"tls_ms,omitempty"`
	FirstByteMS         *int64            `json:"first_byte_ms,omitempty"`
	EndReason           string            `json:"end_reason,omitempty"`
	ResponseOutcome     string            `json:"response_outcome,omitempty"`
	ReceivedEvents      int               `json:"received_events,omitempty"`
	Observations        []Observation     `json:"observations,omitempty"`
	ObservationsDropped int               `json:"observations_dropped,omitempty"`
}

type Snapshot struct {
	Version             int               `json:"version"`
	RequestID           string            `json:"request_id"`
	EdgeRequestID       string            `json:"edge_request_id_untrusted,omitempty"`
	Node                string            `json:"node"`
	Method              string            `json:"method"`
	Path                string            `json:"path"`
	Peer                string            `json:"peer"`
	StartedAt           time.Time         `json:"started_at"`
	EndedAt             *time.Time        `json:"ended_at,omitempty"`
	DownstreamStatus    int               `json:"downstream_status,omitempty"`
	InboundBytesRead    int64             `json:"inbound_body_bytes_read"`
	DownstreamBytes     int64             `json:"downstream_bytes_written"`
	Attempts            []AttemptSnapshot `json:"attempts,omitempty"`
	AttemptsDropped     int               `json:"attempts_dropped,omitempty"`
	Observations        []Observation     `json:"observations,omitempty"`
	ObservationsDropped int               `json:"observations_dropped,omitempty"`
}

// Event is emitted on response headers, attempt end, and final handler exit.
// Header events survive a later body failure; final events include writes that
// may happen after the existing consume log was created.
type Event struct {
	Kind      string           `json:"kind"`
	At        time.Time        `json:"at"`
	RequestID string           `json:"request_id"`
	Node      string           `json:"node"`
	Attempt   *AttemptSnapshot `json:"attempt,omitempty"`
	Summary   *Snapshot        `json:"summary,omitempty"`
}

type Trace struct {
	mu       sync.Mutex
	data     Snapshot
	attempts []*Attempt
	current  *Attempt
	sink     func(Event)
	finished bool
}

type Attempt struct {
	trace *Trace
	data  AttemptSnapshot // protected by trace.mu
}

func New(requestID, edgeID, node, method, path, peer string, sink func(Event)) *Trace {
	path, _, _ = strings.Cut(path, "?")
	return &Trace{data: Snapshot{
		Version: 1, RequestID: safeID(requestID), EdgeRequestID: safeID(edgeID),
		Node: safeID(node), Method: safeID(method), Path: safeText(path, 256),
		Peer: safeText(peer, 128), StartedAt: time.Now().UTC(),
	}, sink: sink}
}

func WithContext(ctx context.Context, trace *Trace) context.Context {
	return context.WithValue(ctx, contextKey{}, trace)
}

func FromContext(ctx context.Context) *Trace {
	if ctx == nil {
		return nil
	}
	trace, _ := ctx.Value(contextKey{}).(*Trace)
	return trace
}

func (t *Trace) BeginAttempt(channelID int, method, endpoint, protocol string) *Attempt {
	if t == nil {
		return nil
	}
	origin := ""
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		origin = safeText(u.Scheme+"://"+u.Host, 256)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.attempts) >= maxEntries {
		t.data.AttemptsDropped++
		t.current = nil
		return nil
	}
	a := &Attempt{trace: t, data: AttemptSnapshot{
		Index: len(t.attempts) + 1, ChannelID: channelID, Method: safeID(method),
		Origin: origin, Protocol: safeID(protocol), StartedAt: time.Now().UTC(),
	}}
	t.attempts = append(t.attempts, a)
	t.current = a
	return a
}

func (t *Trace) CurrentAttempt() *Attempt {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.current
}

// ClearCurrentAttempt starts a relay retry's setup phase. If setup fails
// before any transport starts, it must not mutate the previous attempt.
func (t *Trace) ClearCurrentAttempt() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.current = nil
	t.mu.Unlock()
}

func observation(stage string, err error) Observation {
	o := Observation{At: time.Now().UTC(), Stage: safeID(stage)}
	if err == nil {
		return o
	}
	o.ErrorType = fmt.Sprintf("%T", err)
	var closeError *websocket.CloseError
	if errors.As(err, &closeError) {
		o.ErrorClass = "websocket_close"
		o.WebSocketCloseCode = closeError.Code
		return o
	}
	switch {
	case errors.Is(err, context.Canceled):
		o.ErrorClass = "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		o.ErrorClass = "deadline_exceeded"
	case errors.Is(err, io.ErrUnexpectedEOF):
		o.ErrorClass = "unexpected_eof"
	case errors.Is(err, io.EOF):
		o.ErrorClass = "eof"
	case errors.Is(err, net.ErrClosed):
		o.ErrorClass = "connection_closed"
	case errors.Is(err, syscall.ECONNRESET):
		o.ErrorClass = "connection_reset"
	case errors.Is(err, syscall.EPIPE):
		o.ErrorClass = "broken_pipe"
	case errors.Is(err, syscall.ECONNREFUSED):
		o.ErrorClass = "connection_refused"
	default:
		var ne net.Error
		var dns *net.DNSError
		if errors.As(err, &dns) {
			o.ErrorClass = "dns_error"
		} else if errors.As(err, &ne) && ne.Timeout() {
			o.ErrorClass = "timeout"
		} else {
			o.ErrorClass = "transport_or_processing_error"
		}
	}
	// Error strings can embed URLs, credentials, and provider/user content.
	// Record type and classification only, never err.Error().
	return o
}

func appendObservation(entries *[]Observation, dropped *int, o Observation) {
	if len(*entries) < maxEntries {
		*entries = append(*entries, o)
	} else {
		*dropped++
	}
}

func (t *Trace) Observe(stage string, err error) {
	if t == nil {
		return
	}
	o := observation(stage, err)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.finished {
		appendObservation(&t.data.Observations, &t.data.ObservationsDropped, o)
	}
}

func (a *Attempt) Fail(stage string, err error) {
	if a == nil {
		return
	}
	o := observation(stage, err)
	a.trace.mu.Lock()
	defer a.trace.mu.Unlock()
	appendObservation(&a.data.Observations, &a.data.ObservationsDropped, o)
}

func (t *Trace) AddInboundBytes(n int) {
	if t != nil {
		t.mu.Lock()
		t.data.InboundBytesRead += int64(n)
		t.mu.Unlock()
	}
}
func (t *Trace) AddDownstreamBytes(n int) {
	if t != nil {
		t.mu.Lock()
		t.data.DownstreamBytes += int64(n)
		t.mu.Unlock()
	}
}
func (a *Attempt) AddRequestBytes(n int) {
	if a != nil {
		a.trace.mu.Lock()
		a.data.RequestBytesRead += int64(n)
		a.trace.mu.Unlock()
	}
}
func (a *Attempt) AddUpstreamBytes(n int) {
	if a != nil {
		a.trace.mu.Lock()
		a.data.ResponseBytesRead += int64(n)
		a.trace.mu.Unlock()
	}
}

var idHeaders = []string{
	"X-Oneapi-Request-Id", "X-Request-Id", "Request-Id", "Openai-Request-Id",
	"X-Goog-Request-Id", "Apim-Request-Id", "X-Amzn-Requestid", "X-Amz-Request-Id",
	"X-Correlation-Id",
}

func RequestIDs(header http.Header) map[string]string {
	ids := make(map[string]string)
	for _, name := range idHeaders {
		// Header.Get alone misses non-canonical keys in synthetic/custom clients.
		for key, values := range header {
			if !strings.EqualFold(key, name) {
				continue
			}
			for _, value := range values {
				if id := safeID(strings.TrimSpace(value)); id != "" {
					ids[name] = id
					break
				}
			}
			if ids[name] != "" {
				break
			}
		}
	}
	return ids
}

func PrimaryRequestID(header http.Header) string {
	ids := RequestIDs(header)
	for _, name := range idHeaders {
		// The existing logs.upstream_request_id column is varchar(128).
		// Longer IDs stay intact in the admin audit map, never truncated or
		// allowed to make the existing consume-log INSERT fail.
		if id := ids[name]; id != "" && len(id) <= 128 {
			return id
		}
	}
	return ""
}

func safeID(s string) string {
	if len(s) == 0 || len(s) > 256 {
		return ""
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.:/", r)) {
			return ""
		}
	}
	return s
}

func safeText(s string, limit int) string {
	if len(s) > limit {
		s = s[:limit]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

func (a *Attempt) PrimaryRequestID() string {
	if a == nil {
		return ""
	}
	a.trace.mu.Lock()
	defer a.trace.mu.Unlock()
	return a.data.PrimaryID
}

func (a *Attempt) ReceiveResponse(resp *http.Response) {
	if a == nil || resp == nil {
		return
	}
	now := time.Now().UTC()
	a.trace.mu.Lock()
	a.data.HeadersAt, a.data.Status = &now, resp.StatusCode
	a.data.RequestIDs, a.data.PrimaryID = RequestIDs(resp.Header), PrimaryRequestID(resp.Header)
	a.data.NegotiatedProtocol = safeID(resp.Proto)
	if resp.StatusCode >= 400 {
		appendObservation(&a.data.Observations, &a.data.ObservationsDropped, observation("upstream_http_status", nil))
	}
	a.trace.mu.Unlock()
	a.emit("upstream_headers")
	// A successful websocket handshake has no ordinary response body to wrap.
	if resp.Body != nil && resp.StatusCode != http.StatusSwitchingProtocols {
		resp.Body = &observedBody{ReadCloser: resp.Body, attempt: a, response: true}
	}
}

func (a *Attempt) TraceRequest(req *http.Request) *http.Request {
	if a == nil || req == nil {
		return req
	}
	var dnsStart, connectStart, tlsStart time.Time
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { a.trace.mu.Lock(); dnsStart = time.Now(); a.trace.mu.Unlock() },
		DNSDone: func(info httptrace.DNSDoneInfo) {
			a.trace.mu.Lock()
			if !dnsStart.IsZero() {
				ms := time.Since(dnsStart).Milliseconds()
				a.data.DNSMS = &ms
			}
			a.trace.mu.Unlock()
			if info.Err != nil {
				a.Fail("upstream_dns", info.Err)
			}
		},
		ConnectStart: func(_, _ string) { a.trace.mu.Lock(); connectStart = time.Now(); a.trace.mu.Unlock() },
		ConnectDone: func(_, _ string, err error) {
			a.trace.mu.Lock()
			if !connectStart.IsZero() {
				ms := time.Since(connectStart).Milliseconds()
				a.data.ConnectMS = &ms
			}
			a.trace.mu.Unlock()
			if err != nil {
				a.Fail("upstream_connect", err)
			}
		},
		TLSHandshakeStart: func() { a.trace.mu.Lock(); tlsStart = time.Now(); a.trace.mu.Unlock() },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			a.trace.mu.Lock()
			if !tlsStart.IsZero() {
				ms := time.Since(tlsStart).Milliseconds()
				a.data.TLSMS = &ms
			}
			a.trace.mu.Unlock()
			if err != nil {
				a.Fail("upstream_tls", err)
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			a.trace.mu.Lock()
			a.data.ConnectionReused = info.Reused
			a.trace.mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			a.trace.mu.Lock()
			a.data.TransportWrites++
			a.trace.mu.Unlock()
			if info.Err != nil {
				a.Fail("upstream_request_write", info.Err)
			}
		},
		GotFirstResponseByte: func() {
			a.trace.mu.Lock()
			ms := time.Since(a.data.StartedAt).Milliseconds()
			a.data.FirstByteMS = &ms
			a.trace.mu.Unlock()
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	if req.Body != nil && req.Body != http.NoBody {
		req.Body = &observedBody{ReadCloser: req.Body, attempt: a}
	}
	if original := req.GetBody; original != nil {
		req.GetBody = func() (io.ReadCloser, error) {
			body, err := original()
			if err != nil || body == nil {
				return body, err
			}
			return &observedBody{ReadCloser: body, attempt: a}, nil
		}
	}
	return req
}

type observedBody struct {
	io.ReadCloser
	attempt  *Attempt
	response bool
	closing  bool // protected by trace.mu; distinguishes cleanup-induced read errors
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if !b.response {
		b.attempt.AddRequestBytes(n)
		if err != nil && err != io.EOF {
			b.attempt.Fail("upstream_request_body_read", err)
		}
		return n, err
	}
	b.attempt.AddUpstreamBytes(n)
	b.attempt.trace.mu.Lock()
	closing := b.closing
	if err == io.EOF {
		b.attempt.data.BodyEOF = true
	}
	b.attempt.trace.mu.Unlock()
	if err != nil && err != io.EOF {
		stage := "upstream_body_read"
		if closing {
			stage = "upstream_body_read_after_close"
		}
		b.attempt.Fail(stage, err)
	}
	if err != nil {
		b.attempt.End()
	}
	return n, err
}

func (b *observedBody) Close() error {
	if b.response {
		b.attempt.trace.mu.Lock()
		b.closing = true
		b.attempt.data.BodyClosed = true
		b.attempt.trace.mu.Unlock()
	}
	err := b.ReadCloser.Close()
	if b.response {
		if err != nil {
			b.attempt.Fail("upstream_body_close", err)
		}
		b.attempt.End()
	}
	return err
}

// ObserveSSE extracts only protocol IDs, usage presence and known terminal types.
// Neither input/output text nor raw SSE data is retained.
func (a *Attempt) ObserveSSE(data string) {
	if a == nil {
		return
	}
	// Large image/content frames need no second full JSON scan just for audit.
	// Protocol handlers still determine the authoritative final stream result.
	if len(data) > 512<<10 {
		return
	}
	var id, terminal string
	usage := false
	if data == "[DONE]" {
		terminal = "[DONE]"
	} else if gjson.Valid(data) {
		root := gjson.Parse(data)
		id = root.Get("response.id").String()
		if id == "" {
			id = root.Get("id").String()
		}
		switch event := root.Get("type").String(); event {
		case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "message_stop", "error":
			terminal = event
		}
		usage = root.Get("usage").IsObject() || root.Get("response.usage").IsObject() || root.Get("message.usage").IsObject()
	}
	a.trace.mu.Lock()
	defer a.trace.mu.Unlock()
	if id = safeID(id); id != "" {
		a.data.ResponseObjectID = id
	}
	if terminal != "" {
		a.data.TerminalEvent = terminal
	}
	a.data.UsageObjectObserved = a.data.UsageObjectObserved || usage
}

func (a *Attempt) SetStreamResult(endReason, responseOutcome string, receivedEvents int) {
	if a == nil {
		return
	}
	a.trace.mu.Lock()
	defer a.trace.mu.Unlock()
	a.data.EndReason = safeID(endReason)
	a.data.ResponseOutcome = safeID(responseOutcome)
	a.data.ReceivedEvents = receivedEvents
}

func (a *Attempt) End() {
	if a == nil {
		return
	}
	a.trace.mu.Lock()
	if a.data.EndedAt != nil {
		a.trace.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	a.data.EndedAt = &now
	a.trace.mu.Unlock()
	a.emit("upstream_end")
}

func cloneAttempt(a AttemptSnapshot) AttemptSnapshot {
	a.HeadersAt = clonePointer(a.HeadersAt)
	a.EndedAt = clonePointer(a.EndedAt)
	a.DNSMS = clonePointer(a.DNSMS)
	a.ConnectMS = clonePointer(a.ConnectMS)
	a.TLSMS = clonePointer(a.TLSMS)
	a.FirstByteMS = clonePointer(a.FirstByteMS)
	if a.RequestIDs != nil {
		ids := make(map[string]string, len(a.RequestIDs))
		for k, v := range a.RequestIDs {
			ids[k] = v
		}
		a.RequestIDs = ids
	}
	a.Observations = append([]Observation(nil), a.Observations...)
	return a
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (a *Attempt) emit(kind string) {
	t := a.trace
	if t.sink == nil {
		return
	}
	t.mu.Lock()
	snapshot := cloneAttempt(a.data)
	event := Event{Kind: kind, At: time.Now().UTC(), RequestID: t.data.RequestID, Node: t.data.Node, Attempt: &snapshot}
	t.mu.Unlock()
	t.sink(event)
}

func (t *Trace) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	snapshot := t.data
	snapshot.EndedAt = clonePointer(snapshot.EndedAt)
	snapshot.Observations = append([]Observation(nil), t.data.Observations...)
	snapshot.Attempts = make([]AttemptSnapshot, 0, len(t.attempts))
	for _, a := range t.attempts {
		snapshot.Attempts = append(snapshot.Attempts, cloneAttempt(a.data))
	}
	return snapshot
}

func (t *Trace) EmitSummary(kind string) {
	if t == nil || t.sink == nil {
		return
	}
	snapshot := t.Snapshot()
	if len(snapshot.Attempts) == 0 && len(snapshot.Observations) == 0 {
		return
	}
	t.sink(Event{Kind: kind, At: time.Now().UTC(), RequestID: snapshot.RequestID, Node: snapshot.Node, Summary: &snapshot})
}

func (t *Trace) Finish(status int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	t.finished = true
	now := time.Now().UTC()
	t.data.EndedAt = &now
	t.data.DownstreamStatus = status
	t.mu.Unlock()
	t.EmitSummary("request_end")
}
