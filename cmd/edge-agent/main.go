package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"drift.local/drift-next/internal/service"
)

func main() {
	address := os.Getenv("DRIFT_EDGE_AGENT_ADDR")
	if address == "" {
		address = "127.0.0.1:8081"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("edge-agent listening on %s", address)
	if err := service.Serve(ctx, service.NewHTTPServer("edge-agent", address)); err != nil {
		log.Fatal(err)
	}
}
