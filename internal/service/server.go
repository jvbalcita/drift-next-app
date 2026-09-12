package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"drift.local/drift-next/internal/health"
)

func NewHTTPServer(name, address string) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           health.Handler(name, func() bool { return true }),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func Serve(ctx context.Context, server *http.Server) error {
	errorsCh := make(chan error, 1)
	go func() {
		errorsCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		return nil
	}
}
