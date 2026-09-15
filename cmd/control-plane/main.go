package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/service"
)

func main() {
	// The listener stays on loopback: this is a local operator service. An
	// operator-supplied address is validated rather than trusted, and a
	// non-loopback address fails closed instead of quietly exposing the lab
	// surface to the network.
	address := os.Getenv("DRIFT_CONTROL_PLANE_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	if err := service.ValidateLoopbackAddress(address); err != nil {
		log.Fatalf("refusing to start: %v (set DRIFT_CONTROL_PLANE_ADDR to 127.0.0.1:PORT, [::1]:PORT, or localhost:PORT)", err)
	}

	// Lab mode requires an explicit opt-in plus a configured adb path; anything
	// else yields a deterministic mock service that never reaches a device.
	labService, err := lab.NewServiceFromEnv(os.LookupEnv)
	if err != nil {
		log.Fatal(err)
	}
	labMode := "mock"
	labToken := strings.TrimSpace(os.Getenv(lab.EnvLabToken))
	if lab.LabModeRequested(os.LookupEnv) {
		labMode = "lab"
		// Loopback keeps the surface off the network, but it does not
		// distinguish a hostile local process from the console. Real-device
		// mode therefore requires a shared secret as well.
		if labToken == "" {
			log.Fatalf("refusing to start lab mode: %s must be set to a non-empty local lab token", lab.EnvLabToken)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := service.NewHTTPServer("control-plane", address, service.LabAdapterRoute(labService, labToken))
	log.Printf("control-plane listening on %s with lab adapter in %s mode (lab token %s)", address, labMode, tokenState(labToken))
	if err := service.Serve(ctx, server); err != nil {
		log.Fatal(err)
	}
}

// tokenState reports whether the lab route is guarded without ever rendering
// the token itself.
func tokenState(token string) string {
	if token == "" {
		return "not configured; loopback-only"
	}
	return "required"
}
