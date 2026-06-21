package observability

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
)

// responseWriter captures the status code and body size while forwarding the
// response to the real client.
//
// Why this exists:
// The metrics middleware needs to know what status code and response size
// were produced by the handler.
//
// Why the wrapper is kept small:
// It should only observe, not change response behavior.
type responseWriter struct {
	http.ResponseWriter

	statusCode   int
	bytesWritten int64
	wroteHeader  bool
}

// Header returns the underlying response headers.
func (w *responseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

// WriteHeader stores the status code and forwards it to the client.
func (w *responseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}

	w.wroteHeader = true
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write stores the response size and forwards the bytes to the client.
func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	n, err := w.ResponseWriter.Write(p)
	w.bytesWritten += int64(n)
	return n, err
}

// Flush forwards flush behavior when the underlying writer supports it.
func (w *responseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack forwards hijacking when the underlying writer supports it.
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacking not supported")
	}
	return hijacker.Hijack()
}

// Push forwards HTTP/2 server push when supported.
func (w *responseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

// Unwrap returns the underlying writer.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// var (
// 	_ http.Flusher  = (*responseWriter)(nil)
// 	_ http.Hijacker = (*responseWriter)(nil)
// 	_ http.Pusher   = (*responseWriter)(nil)
// )
