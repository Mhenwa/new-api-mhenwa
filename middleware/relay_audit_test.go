package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/relaytrace"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayAuditMetadataPrivacyAndSnapshots(t *testing.T) {
	var events []relaytrace.Event
	trace := relaytrace.New("local-id", "edge-id", "node-b", "POST", "/v1/responses?api_key=secret-query", "127.0.0.1:1234",
		func(event relaytrace.Event) { events = append(events, event) })
	attempt := trace.BeginAttempt(88, "POST", "https://user:secret-password@upstream.example/v1/responses?key=secret-query", "http")
	response := &http.Response{StatusCode: 200, Proto: "HTTP/2.0", Header: http.Header{
		"x-request-id": {"provider-uuid"}, "Request-Id": {"provider-secondary"},
		"Authorization": {"Bearer secret-credential"}, "Set-Cookie": {"secret-cookie"},
	}, Body: io.NopCloser(strings.NewReader("payload"))}
	attempt.ReceiveResponse(response)
	require.Len(t, events, 1)
	assert.Equal(t, "provider-uuid", events[0].Attempt.PrimaryID)
	attempt.ObserveSSE(`{"type":"response.completed","response":{"id":"resp_object","usage":{"input_tokens":4},"output":[{"text":"secret-output"}]}}`)
	attempt.Fail("upstream_body_read", &url.Error{Op: "Get", URL: "https://upstream.example/?key=secret-url", Err: errors.New("secret-error-body")})
	attempt.SetStreamResult("client_gone", "completed", 3)
	trace.Observe("downstream_context", context.Canceled)
	attempt.End()
	trace.Finish(200)

	snapshot := trace.Snapshot()
	require.Len(t, snapshot.Attempts, 1)
	assert.Equal(t, "https://upstream.example", snapshot.Attempts[0].Origin)
	assert.Equal(t, "/v1/responses", snapshot.Path)
	assert.Equal(t, "resp_object", snapshot.Attempts[0].ResponseObjectID)
	assert.True(t, snapshot.Attempts[0].UsageObjectObserved)
	assert.Equal(t, "completed", snapshot.Attempts[0].ResponseOutcome)
	assert.Equal(t, "client_gone", snapshot.Attempts[0].EndReason)
	assert.Empty(t, events[0].Attempt.ResponseObjectID, "header event must remain an immutable early snapshot")
	assert.Equal(t, "request_end", events[len(events)-1].Kind)

	encoded, err := common.Marshal(events)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "secret-")
	assert.NotContains(t, string(encoded), "Authorization")
	assert.NotContains(t, string(encoded), "Set-Cookie")
	assert.Contains(t, string(encoded), "provider-secondary")
	assert.Contains(t, string(encoded), "context_canceled")

	// A consumer may modify its snapshot without mutating the live request.
	snapshot.Attempts[0].RequestIDs["X-Request-Id"] = "changed"
	snapshot.Attempts[0].Observations[0].Stage = "changed"
	assert.Equal(t, "provider-uuid", trace.Snapshot().Attempts[0].RequestIDs["X-Request-Id"])
	assert.Equal(t, "upstream_body_read", trace.Snapshot().Attempts[0].Observations[0].Stage)
	trace.ClearCurrentAttempt()
	assert.Nil(t, trace.CurrentAttempt())
	assert.Equal(t, "provider-uuid", trace.Snapshot().Attempts[0].PrimaryID)
}

func TestRelayAuditPanicStillReachesExistingRecovery(t *testing.T) {
	t.Setenv("RELAY_AUDIT_ENABLED", "true")
	var trace *relaytrace.Trace
	panicValue := errors.New("handler panic")
	router := gin.New()
	router.Use(func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				assert.Same(t, panicValue, recovered)
				c.AbortWithStatus(500)
			}
		}()
		c.Next()
	})
	router.Use(RequestId())
	router.GET("/v1/models", func(c *gin.Context) {
		trace = relaytrace.FromContext(c.Request.Context())
		panic(panicValue)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/v1/models", nil))
	assert.Equal(t, 500, recorder.Code)
	require.NotNil(t, trace)
	snapshot := trace.Snapshot()
	assert.Equal(t, 500, snapshot.DownstreamStatus)
	require.Len(t, snapshot.Observations, 1)
	assert.Equal(t, "local_handler_panic", snapshot.Observations[0].Stage)
}

func TestRelayAuditRejectsUnsafeIDsAndBoundsMemory(t *testing.T) {
	for _, value := range []string{"bad\nlog-line", "contains space", strings.Repeat("x", 257)} {
		assert.Empty(t, relaytrace.PrimaryRequestID(http.Header{"X-Request-Id": {value}}))
	}
	trace := relaytrace.New("local-id", "edge\ninjected", "node", "POST", "/v1/responses", "peer", nil)
	for range 40 {
		trace.Observe("stage", context.Canceled)
		trace.BeginAttempt(1, "POST", "https://upstream.example", "http")
	}
	snapshot := trace.Snapshot()
	assert.Empty(t, snapshot.EdgeRequestID)
	assert.Nil(t, trace.CurrentAttempt(), "a dropped attempt must not update the preceding attempt")
	assert.Len(t, snapshot.Attempts, 32)
	assert.Equal(t, 8, snapshot.AttemptsDropped)
	assert.Len(t, snapshot.Observations, 32)
	assert.Equal(t, 8, snapshot.ObservationsDropped)
	longID := strings.Repeat("a", 129)
	assert.Empty(t, relaytrace.PrimaryRequestID(http.Header{"X-Request-Id": {longID}}))
	assert.Equal(t, longID, relaytrace.RequestIDs(http.Header{"X-Request-Id": {longID}})["X-Request-Id"])
	closeTrace := relaytrace.New("local", "", "node", "WS", "/v1/responses", "peer", nil)
	closeTrace.Observe("downstream_websocket_read", &websocket.CloseError{Code: 1006, Text: "secret-close-reason"})
	closeSnapshot := closeTrace.Snapshot()
	assert.Equal(t, 1006, closeSnapshot.Observations[0].WebSocketCloseCode)
	encoded, err := common.Marshal(closeSnapshot)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "secret-close-reason")

	var disabled *relaytrace.Trace
	assert.Nil(t, disabled.CurrentAttempt())
	assert.Nil(t, disabled.BeginAttempt(1, "POST", "https://example.com", "http"))
	disabled.Observe("stage", context.Canceled)
	disabled.Finish(200)
}

func TestRelayAuditMiddlewareKeepsLocalIdentityAndCancellation(t *testing.T) {
	t.Setenv("RELAY_AUDIT_ENABLED", "true")
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var trace *relaytrace.Trace
	var localID string
	router := gin.New()
	router.Use(RequestId())
	router.POST("/v1/responses", func(c *gin.Context) {
		trace = relaytrace.FromContext(c.Request.Context())
		require.NotNil(t, trace)
		localID = c.GetString(common.RequestIdKey)
		assert.Equal(t, localID, c.Request.Context().Value(common.RequestIdKey))
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		assert.Equal(t, "request", string(body))
		trace.BeginAttempt(88, "POST", "https://provider.example", "http")
		_, err = c.Writer.WriteString("response")
		require.NoError(t, err)
		cancel()
	})
	request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader("request")).WithContext(requestCtx)
	request.Header.Set(common.RequestIdKey, "client-cannot-overwrite-local-id")
	request.Header.Set(relaytrace.EdgeRequestIDHeader, "edge-123")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	assert.Equal(t, "response", recorder.Body.String())
	assert.Equal(t, localID, recorder.Header().Get(common.RequestIdKey))
	assert.NotEqual(t, "client-cannot-overwrite-local-id", localID)
	snapshot := trace.Snapshot()
	assert.Equal(t, "edge-123", snapshot.EdgeRequestID)
	assert.EqualValues(t, len("request"), snapshot.InboundBytesRead)
	assert.EqualValues(t, len("response"), snapshot.DownstreamBytes)
	require.NotNil(t, snapshot.EndedAt)
	assert.Equal(t, 200, snapshot.DownstreamStatus)
	require.Len(t, snapshot.Observations, 1)
	assert.Equal(t, "downstream_context", snapshot.Observations[0].Stage)
	assert.Equal(t, "context_canceled", snapshot.Observations[0].ErrorClass)

	t.Setenv("RELAY_AUDIT_ENABLED", "false")
	router = gin.New()
	router.Use(RequestId())
	router.GET("/v1/models", func(c *gin.Context) {
		assert.Nil(t, relaytrace.FromContext(c.Request.Context()))
		c.Status(http.StatusOK)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/models", nil))
}

type failingAuditHTTPWriter struct {
	*httptest.ResponseRecorder
	err      error
	deadline time.Time
}

func (w *failingAuditHTTPWriter) Write(p []byte) (int, error) { return min(2, len(p)), w.err }
func (w *failingAuditHTTPWriter) FlushError() error           { return w.err }
func (w *failingAuditHTTPWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestRelayAuditWriterPreservesPartialWritesFlushAndDeadline(t *testing.T) {
	failure := errors.New("secret-network-error-text")
	underlying := &failingAuditHTTPWriter{ResponseRecorder: httptest.NewRecorder(), err: failure}
	c, _ := gin.CreateTestContext(underlying)
	trace := relaytrace.New("local", "", "node", "POST", "/v1/responses", "peer", nil)
	writer := &relayAuditResponseWriter{ResponseWriter: c.Writer, trace: trace}
	n, err := writer.Write([]byte("partial-response"))
	require.ErrorIs(t, err, failure)
	assert.Equal(t, 2, n)
	require.ErrorIs(t, http.NewResponseController(writer).Flush(), failure)
	deadline := time.Now().Add(time.Minute)
	require.NoError(t, http.NewResponseController(writer).SetWriteDeadline(deadline))
	assert.Equal(t, deadline, underlying.deadline)
	assert.EqualValues(t, 2, trace.Snapshot().DownstreamBytes)
	observations := trace.Snapshot().Observations
	require.Len(t, observations, 2)
	assert.Equal(t, "downstream_write", observations[0].Stage)
	assert.Equal(t, "downstream_flush", observations[1].Stage)
}
