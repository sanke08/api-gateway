package server

import (
	"net/http"

	"github.com/sanke08/api_gateway/internal/middleware"
)

type Server struct {
	httpServer *http.Server
}

// Initiallize server
func New(addr string) *Server {

	mux := http.NewServeMux()

	s := &Server{
		httpServer: &http.Server{
			Addr:    ":" + addr,
			Handler: middleware.LoggingMiddleware(mux),
		},
	}

	return s
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}
