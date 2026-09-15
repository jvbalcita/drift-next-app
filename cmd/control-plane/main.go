package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/artifacts/cas"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/registration"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/service"
	store "drift.local/drift-next/internal/store/sqlite"
	transportconnect "drift.local/drift-next/internal/transport/connect"
)

const (
	envControlPlaneDB  = "DRIFT_CONTROL_PLANE_DB"
	envArtifactCASRoot = "DRIFT_ARTIFACT_CAS_ROOT"
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

	labToken := strings.TrimSpace(os.Getenv(lab.EnvLabToken))
	if lab.LabModeRequested(os.LookupEnv) && labToken == "" {
		// Loopback keeps the surface off the network, but it does not
		// distinguish a hostile local process from the console. Real-device
		// mode therefore requires a shared secret as well.
		log.Fatalf("refusing to start lab mode: %s must be set to a non-empty local lab token", lab.EnvLabToken)
	}

	// Keep the interface typed as LabRegistrationStore so a nil *store.DB is not
	// stored as a non-nil interface value.
	var durableStore transportconnect.LabRegistrationStore
	var artifactAPI transportconnect.ArtifactAPI
	var labOpts []lab.Option
	dbPath := strings.TrimSpace(os.Getenv(envControlPlaneDB))
	if dbPath != "" {
		db, openErr := store.Open(context.Background(), dbPath, store.Options{})
		if openErr != nil {
			log.Fatalf("refusing to start: open %s: %v", envControlPlaneDB, openErr)
		}
		defer func() { _ = db.Close() }()
		workspaceID := organizations.WorkspaceID("workspace-lab-local")
		if createErr := store.NewWorkspaceService(db).Create(context.Background(), organizations.Workspace{
			ID: workspaceID, Name: "Local Lab", State: organizations.WorkspaceActive,
		}, "system", "control-plane"); createErr != nil && platformerrors.CodeOf(createErr) != platformerrors.CodeConflict {
			log.Fatalf("refusing to start: ensure lab workspace: %v", createErr)
		}
		durableStore = db
		log.Printf("control-plane durable lab registration enabled at %s", dbPath)

		casRoot := strings.TrimSpace(os.Getenv(envArtifactCASRoot))
		if casRoot == "" {
			casRoot = filepath.Join(filepath.Dir(dbPath), "artifacts", "cas")
		}
		if !filepath.IsAbs(casRoot) {
			absRoot, absErr := filepath.Abs(casRoot)
			if absErr != nil {
				log.Fatalf("refusing to start: resolve %s: %v", envArtifactCASRoot, absErr)
			}
			casRoot = absRoot
		}
		casStore, casErr := cas.Open(casRoot)
		if casErr != nil {
			log.Fatalf("refusing to start: open artifact CAS at %s: %v", casRoot, casErr)
		}
		artifactService, artifactErr := artifacts.NewService(store.NewArtifactService(db), casStore)
		if artifactErr != nil {
			log.Fatalf("refusing to start: construct artifact service: %v", artifactErr)
		}
		artifactAPI = artifactService
		labOpts = append(labOpts, lab.WithEvidencePersister(artifacts.CapturePersister{
			Service:   artifactService,
			Workspace: workspaceID,
		}))
		log.Printf("control-plane local artifact CAS enabled at %s", casStore.Root())
	}

	// Lab mode requires an explicit opt-in plus a configured adb path; anything
	// else yields a deterministic mock service that never reaches a device.
	labService, err := lab.NewServiceFromEnv(os.LookupEnv, labOpts...)
	if err != nil {
		log.Fatal(err)
	}
	labMode := "mock"
	if lab.LabModeRequested(os.LookupEnv) {
		labMode = "lab"
	}

	allowedPorts := []uint16{5555}
	registrationService := registration.NewService(registration.Config{
		MaxRegisteredDevices: 1,
		Probe: registration.LabStatusProbe{
			Source:       labService,
			AllowedPorts: allowedPorts,
		},
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	routes := []service.Route{
		service.LabAdapterRoute(labService, labToken),
		service.LabRegistrationRouteWithStore(registrationService, durableStore, labToken, allowedPorts),
	}
	if artifactAPI != nil {
		routes = append(routes, service.ArtifactRoute(artifactAPI, labToken))
	}
	server := service.NewHTTPServer("control-plane", address, routes...)
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
