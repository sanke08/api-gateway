package main

import (
	"log"

	"github.com/sanke08/api_gateway/internal/config"
	"github.com/sanke08/api_gateway/internal/observability"
	"github.com/sanke08/api_gateway/internal/server"
)

func main() {

	// Initiallize Logger
	observability.InitLogger()

	// Load Config
	cfg, err := config.Load()
	if err != nil {
		observability.Error("Failed to load config", "error", err)
		log.Fatal(err)
	}

	// Initiallize HTTP server
	srv := server.New(cfg.Port)

	log.Printf("Starting server on port %s", cfg.Port)

	if err := srv.Start(); err != nil {
		observability.Error("Failed to start server", "error", err)
		log.Fatal(err)
	}
}
