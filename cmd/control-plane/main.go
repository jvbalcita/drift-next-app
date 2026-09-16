package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	dbmigrations "drift.local/drift-next/db/migrations"
	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/artifacts/cas"
	"drift.local/drift-next/internal/discovery"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
	migrationrunner "drift.local/drift-next/internal/platform/migrations"
	"drift.local/drift-next/internal/product"
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

	labToken := strings.TrimSpace(os.Getenv(lab.EnvRuntimeToken))
	if labToken == "" {
		labToken = strings.TrimSpace(os.Getenv(lab.EnvLabToken))
	}
	if lab.LabModeRequested(os.LookupEnv) && labToken == "" {
		// Loopback keeps the surface off the network, but it does not
		// distinguish a hostile local process from the console. Real-device
		// mode therefore requires a shared secret as well.
		log.Fatalf("refusing to start lab mode: %s must be set to a non-empty local lab token", lab.EnvLabToken)
	}

	// Keep the interface typed as ArtifactAPI so a nil *store.DB-derived value
	// is not stored as a non-nil interface value.
	var artifactAPI transportconnect.ArtifactAPI
	var productHandlers *transportconnect.ProductHandlers
	var labOpts []lab.Option
	dbPath := strings.TrimSpace(os.Getenv(envControlPlaneDB))
	if dbPath == "" {
		dbPath = product.DefaultControlPlaneDBPath()
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(dbPath), 0o700); mkdirErr != nil {
		log.Fatalf("refusing to start: create control-plane data directory: %v", mkdirErr)
	}
	// drift and the control plane share this database and can run at different
	// revisions, so a stale binary must refuse to serve a schema that a newer
	// build already migrated. This runs before any service is constructed.
	if guardErr := migrationrunner.CheckLedgerNotAhead(context.Background(), dbPath, dbmigrations.SQLiteFiles); guardErr != nil {
		log.Fatalf("refusing to start: %v", guardErr)
	}
	db, openErr := store.Open(context.Background(), dbPath, store.Options{})
	if openErr != nil {
		log.Fatalf("refusing to start: open control-plane sqlite: %v", openErr)
	}
	defer func() { _ = db.Close() }()
	workspaceID := organizations.WorkspaceID("workspace-lab-local")
	if createErr := store.NewWorkspaceService(db).Create(context.Background(), organizations.Workspace{
		ID: workspaceID, Name: "Local Workspace", State: organizations.WorkspaceActive,
	}, "system", "control-plane"); createErr != nil && platformerrors.CodeOf(createErr) != platformerrors.CodeConflict {
		log.Fatalf("refusing to start: ensure lab workspace: %v", createErr)
	}
	log.Printf("control-plane durable store enabled at %s", dbPath)

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    lab.LabModeRequested(os.LookupEnv),
		Enumerator: product.NewLabRuntimeEnumerator(labService),
	})
	productHandlers = transportconnect.NewProductHandlers(db, scanner)
	actionRuntime := execution.NewRegistry(labService, db)
	productHandlers.Action.SetExecutor(actionRuntime)
	defer func() { _ = actionRuntime.Close() }()

	// One bounded auto-scan of the default network profile per process start.
	// It reuses the manual scan machinery, is cancelled by the same shutdown
	// context as the listener, is owned by main (see the wait below), and never
	// aborts startup: every outcome is recorded and logged instead.
	autoScan := product.NewStartupAutoScanner(product.StartupAutoScanConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Profiles:   store.NewNetworkProfileRepository(db),
		Runner:     discovery.NewService(db, scanner),
		Recorder:   db,
		StartedAt:  time.Now().UTC(),
	})
	autoScanDone := make(chan struct{})
	go func() {
		defer close(autoScanDone)
		log.Printf("%s", autoScan.Run(ctx).Report())
	}()

	routes := []service.Route{
		service.LabAdapterRoute(labService, labToken),
	}
	if artifactAPI != nil {
		routes = append(routes, service.ArtifactRoute(artifactAPI, labToken))
	}
	routes = append(routes, service.ProductRoutes(productHandlers, labToken)...)
	// The typed device input surface. It is mounted only when the whole path was
	// constructed: an empty Route mounts nothing, so a missing dependency degrades
	// to "no input surface" rather than to a route that cannot dispatch anything
	// (AGENTS.md section 6).
	inputMounted := false
	if inputRoute := deviceInputRoute(labService, actionRuntime, db, labToken); inputRoute.Path != "" {
		routes = append(routes, inputRoute)
		inputMounted = true
	}
	server := service.NewHTTPServer("control-plane", address, routes...)
	log.Printf("control-plane listening on %s with lab adapter in %s mode (lab token %s, device input surface %s)", address, labMode, tokenState(labToken), mountState(inputMounted))
	serveErr := service.Serve(ctx, server)
	// The startup scan is owned work, not a detached worker: wait for it to
	// obey cancellation before the process returns.
	<-autoScanDone
	if serveErr != nil {
		log.Fatal(serveErr)
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

// mountState reports whether the device input surface was mounted. It does not
// imply that a mounted surface will accept an input: the kernel still decides
// every dispatch.
func mountState(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "not mounted; the device input path was not constructed"
}

// inputObservationOperator is the identity the composition reads a device's
// postcondition observation under. It is the composition's own identity rather
// than a user's: the operator who asked for the action is the actor the kernel
// records on the attempt, and this read happens on that action's behalf. Naming a
// service identity keeps the read attributable without inventing a person.
const inputObservationOperator = "control-plane-input-observer"

// observationSource builds the postcondition observer's per-device observation
// read over the lab boundary's read-only observation adapter. The adapter names
// the serial on every capture, so the read is authorized against the attached
// transports rather than against session state.
func observationSource(labService *lab.Service) execution.ObservationSourceFactory {
	return func(serial string) (execution.ObservationSource, error) {
		return lab.NewObservationAdapter(labService, serial, inputObservationOperator)
	}
}

// deviceInputRoute builds the typed device input route, or an empty Route when the
// path cannot be constructed.
//
// Every dependency is checked, and every absence returns an empty Route, which
// mounts nothing at all. That is deliberate rather than defensive: a route whose
// dispatcher cannot dispatch is worse than no route, because it advertises a
// surface that cannot work (AGENTS.md section 6). Each reason is logged rather
// than swallowed, so a deployment that expected the surface can see which
// dependency was missing instead of finding no route and no explanation.
//
// The composed path, in the order a request travels it: the transport is the
// device's own allow-listed runner (4a); the readiness probe judges the device from
// the lab boundary's attached set (4b); the postcondition observer reads the
// device's current observation (slice 3); the dispatcher carries the whole P7
// contract with the evidence recorder bound; and the application boundary resolves
// the device to its serial and assigns the attempt identity (4c), which is what
// satisfies the port this route serves.
func deviceInputRoute(labService *lab.Service, resolver *execution.Registry, db *store.DB, token string) service.Route {
	transport, err := execution.NewInputTransportFromAllowlisted(labService.DeviceTransport())
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	observer, err := execution.NewObservationPostconditionObserver(resolver, observationSource(labService))
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	deviceState, err := execution.NewTransportObserverFromAttached(labService)
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	dispatcher, err := execution.NewInputDispatcher(
		store.NewActionService(db),
		execution.NewStoreControlProbe(db, deviceState),
		observer,
		transport,
		// The resolver exists, but no surface can register a value with it yet:
		// where the plaintext enters is a boundary decision of its own, carded
		// as ARC-107. Wiring a registry nothing can register into would leave
		// typed text refusing for a different reason than it does now, so a
		// typed-text payload keeps failing closed at the boundary instead of
		// being dispatched with a value nobody released.
		nil,
		execution.WithEvidenceRecorder(store.NewActionEvidenceService(db)),
	)
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	boundary, err := transportconnect.NewDeviceInputBoundary(dispatcher, resolver, ids.NewRandom())
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	return service.DeviceInputRoute(boundary, token)
}
