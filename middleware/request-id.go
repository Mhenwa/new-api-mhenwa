package middleware

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/relaytrace"
	"github.com/gin-gonic/gin"
)

func RequestId() func(c *gin.Context) {
	return func(c *gin.Context) {
		id := common.NewRequestId()
		c.Set(common.RequestIdKey, id)
		ctx := context.WithValue(c.Request.Context(), common.RequestIdKey, id)
		c.Request = c.Request.WithContext(ctx)
		c.Header(common.RequestIdKey, id)
		if relaytrace.Enabled() {
			audit := relaytrace.New(id, c.GetHeader(relaytrace.EdgeRequestIDHeader), common.NodeName,
				c.Request.Method, c.Request.URL.Path, c.Request.RemoteAddr, func(event relaytrace.Event) {
					if data, err := common.Marshal(event); err == nil {
						logger.LogInfo(ctx, "relay_audit "+string(data))
					}
				})
			requestContext := relaytrace.WithContext(ctx, audit)
			c.Request = c.Request.WithContext(requestContext)
			if c.Request.Body != nil && c.Request.Body != http.NoBody {
				c.Request.Body = &auditRequestBody{ReadCloser: c.Request.Body, trace: audit}
			}
			writer := &relayAuditResponseWriter{ResponseWriter: c.Writer, trace: audit}
			c.Writer = writer
			// Capture cancellation at the time observed, rather than assuming
			// that it was the cause of a later read error during cleanup.
			observed := make(chan struct{})
			stop := context.AfterFunc(requestContext, func() {
				audit.Observe("downstream_context", requestContext.Err())
				close(observed)
			})
			defer func() {
				recovered := recover()
				if !stop() {
					<-observed
				}
				status := c.Writer.Status()
				if recovered != nil {
					audit.Observe("local_handler_panic", nil)
					// Recovery is outside this middleware. Preserve a previously
					// committed status; otherwise record its pending 500.
					if !c.Writer.Written() {
						status = http.StatusInternalServerError
					}
				}
				if writer.hijacked {
					status = http.StatusSwitchingProtocols
				}
				audit.Finish(status)
				if recovered != nil {
					panic(recovered)
				}
			}()
		}
		c.Next()
	}
}

type auditRequestBody struct {
	io.ReadCloser
	trace *relaytrace.Trace
}

func (b *auditRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.trace.AddInboundBytes(n)
	if err != nil && err != io.EOF {
		b.trace.Observe("downstream_request_read", err)
	}
	return n, err
}

type relayAuditResponseWriter struct {
	gin.ResponseWriter
	trace    *relaytrace.Trace
	hijacked bool
}

func (w *relayAuditResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.Hijack()
	if err == nil {
		w.hijacked = true
	}
	w.trace.Observe("downstream_hijack", err)
	return conn, rw, err
}

func (w *relayAuditResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.trace.AddDownstreamBytes(n)
	if err != nil {
		w.trace.Observe("downstream_write", err)
	}
	return n, err
}

func (w *relayAuditResponseWriter) WriteString(s string) (int, error) {
	n, err := w.ResponseWriter.WriteString(s)
	w.trace.AddDownstreamBytes(n)
	if err != nil {
		w.trace.Observe("downstream_write", err)
	}
	return n, err
}

// Unwrap preserves ResponseController deadlines through the stream wrappers.
func (w *relayAuditResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Gin's Flush discards the underlying error. ResponseController calls this
// method when supported, preserving an observable flush failure.
func (w *relayAuditResponseWriter) FlushError() error {
	w.ResponseWriter.WriteHeaderNow()
	// Gin exposes Unwrap but its Flush swallows the underlying flush error.
	// Skip that layer when available while retaining its header accounting.
	var target http.ResponseWriter = w.ResponseWriter
	if unwrapper, ok := target.(interface{ Unwrap() http.ResponseWriter }); ok {
		target = unwrapper.Unwrap()
	}
	err := http.NewResponseController(target).Flush()
	if err != nil {
		w.trace.Observe("downstream_flush", err)
	}
	return err
}

func (w *relayAuditResponseWriter) Flush() { _ = w.FlushError() }
