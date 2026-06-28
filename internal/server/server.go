package server

import (
	"net/http"
)

type Server struct {
	httpServer *http.Server
}

// New creates an HTTP server that serves the given handler.
//
// Why the handler is passed in (not built here):
// main.go owns the full wiring — DB, repos, services, router, middleware.
// The server's only job is to bind a port and call ListenAndServe.
// Keeping those two concerns separate means you can swap the handler in tests
// without touching any network code.
func New(addr string, handler http.Handler) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:    ":" + addr,
			Handler: handler,
		},
	}
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}
