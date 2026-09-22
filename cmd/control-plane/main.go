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
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/connection"
	devicediagnostics "drift.local/drift-next/internal/edge/diagnostics"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/mirror"
	"drift.local/drift-next/internal/media"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
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
	// envTransferRoot points at the host directory a catalogued device file
	// operation materializes a push under. It is a deployment input rather than
	// a constant because the path has to be one the device adapter's own
	// allow-list admits, and only the deployment knows where its data lives.
	envTransferRoot = "DRIFT_DEVICE_TRANSFER_ROOT"
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

	// One adapter enumeration over the lab runtime, read by both the startup
	// scan and the post-launch watcher below: a device that attaches after
	// launch is seen through the same view the scan observes, not a second one.
	enumerator := product.NewLabRuntimeEnumerator(labService)
	scanner := discovery.NewAuthorizedLabScanner(discovery.LabScannerConfig{
		Authorized: true,
		LabMode:    lab.LabModeRequested(os.LookupEnv),
		Enumerator: enumerator,
	})
	productHandlers = transportconnect.NewProductHandlers(db, scanner)
	if transport := labService.DeviceTransport(); transport != nil {
		productHandlers.Device.SetDiagnosticsCollector(devicediagnostics.NewCollector(transport))
	}
	actionRuntime := execution.NewRegistry(labService, db)
	productHandlers.Action.SetExecutor(actionRuntime)
	defer func() { _ = actionRuntime.Close() }()

	// One discovery service, and therefore one serial-keyed upsert: the startup
	// scan and the post-launch watcher below both record through this instance,
	// so a device that attaches after launch resolves to the identity it already
	// has instead of minting a second one (AGENTS.md section 2).
	discoveryService := discovery.NewService(db, scanner)

	// One bounded auto-scan of the default network profile per process start.
	// It reuses the manual scan machinery, is cancelled by the same shutdown
	// context as the listener, is owned by main (see the wait below), and never
	// aborts startup: every outcome is recorded and logged instead.
	autoScan := product.NewStartupAutoScanner(product.StartupAutoScanConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Profiles:   store.NewNetworkProfileRepository(db),
		Runner:     discoveryService,
		Recorder:   db,
		StartedAt:  time.Now().UTC(),
	})
	autoScanDone := make(chan struct{})
	go func() {
		defer close(autoScanDone)
		log.Printf("%s", autoScan.Run(ctx).Report())
	}()

	// One bounded sweep of the mirror sessions a previous process left open. A
	// mirror session carries no expiry, so a session whose process died stays
	// 'active' with no finished_at and reports running work that is not running -
	// silently, once per restart. This finds them, ends each one with the plane's
	// own class for the engine that was carrying it going away, names every
	// session it ended in the startup line, and deletes nothing: these rows are
	// history. It is cancelled by the same shutdown context as the listener and is
	// owned by main (see the wait below). It never aborts startup, and it is
	// bounded by the moment this process started: only a session opened BEFORE
	// this start can be a previous process's leftover.
	mirrorSweep := product.NewStartupMirrorSweep(product.StartupMirrorSweepConfig{
		Workspaces: store.NewWorkspaceRepository(db),
		Sessions:   store.NewMirrorService(db),
		StartedAt:  time.Now().UTC(),
	})
	mirrorSweepDone := make(chan struct{})
	go func() {
		defer close(mirrorSweepDone)
		log.Printf("%s", mirrorSweep.Run(ctx).Report())
	}()

	// Post-launch arrivals: the startup scan covers what is attached when the
	// process starts, and this covers what attaches after it. The watcher polls
	// the same adapter enumeration the scan reads, is cancelled by the same
	// shutdown context as the listener, is owned by main (see the wait below),
	// and never aborts startup: every arrival, departure, and failed poll is
	// reported as it happens rather than held back to shutdown.
	//
	// Every arrival it observes is recorded through the same serial-keyed upsert
	// the startup scan writes with, on the poll's own bounded context, so the
	// device is in the registry the console reads by the time the next poll runs -
	// no operator action, no scan. The watcher still owns no scan, no device
	// execution, and no worker of its own: recording an arrival is that one
	// bounded write, and it happens on the poll that observed it.
	transportWatch := product.NewTransportWatcher(product.TransportWatcherConfig{
		Enumerator: enumerator,
		Sink: func(ctx context.Context, arrivals []discovery.ObservedDevice) error {
			_, recordErr := discoveryService.RecordArrivals(ctx, workspaceID, arrivals, product.WatcherActorType, product.WatcherActorID)
			return recordErr
		},
		// A departure is recorded the same way an arrival is: on the poll that
		// observed it, on that poll's bounded context. Without this the watcher
		// counts a departure and discards it, and a device unplugged an hour ago
		// still answers as present to every reader of the registry.
		DepartureSink: func(ctx context.Context, departures []discovery.ObservedDevice) error {
			return discoveryService.RecordDepartures(ctx, workspaceID, departures, product.WatcherActorType, product.WatcherActorID)
		},
	})
	transportWatchDone := make(chan struct{})
	go func() {
		defer close(transportWatchDone)
		log.Printf("%s", transportWatch.Run(ctx).Report())
	}()

	// The frame engine: one owned worker that captures a bounded still per
	// subscribed device on a bounded interval, through the same allow-listed
	// capture path the one-shot observation uses. It starts on the process's
	// shutdown context, is cancelled by it, and is awaited below before the
	// process returns. Nothing is subscribed at startup, so it captures nothing
	// until something subscribes: a subscription is what starts the work, and an
	// unsubscribed device is not captured at all. This is a bounded snapshot
	// engine, not a video transport - it holds the most recent still per
	// subscribed device and never claims to be continuous.
	//
	// It is the engine behind the console's fleet grid, which is why the grid's
	// cost is decided HERE and nowhere else: the cadence, the still level and the
	// sweep bound are read from the deployment's own configuration, stated in a
	// startup line the operator can read back, and handed to the engine that
	// spends them. A deployment that mistyped one of them is a startup diagnosis
	// rather than a grid whose tiles refresh at a rate nobody chose - and the
	// route is left unmounted, so a console is told the plane has no grid rather
	// than shown one that is not the one it configured.
	gridSettings, gridSettingsErr := media.GridSettingsFromEnv(os.LookupEnv)
	if gridSettingsErr != nil {
		log.Printf("grid preview surface not mounted: %v", gridSettingsErr)
	} else {
		log.Printf("%s", gridSettings.Report())
	}
	frameEngine, frameEngineErr := media.NewFrameEngine(media.FrameEngineConfig{
		Capturer:           labService.FrameTransport(),
		Interval:           gridSettings.Cadence,
		Profile:            gridSettings.Profile,
		MaxSubscribers:     gridSettings.MaxDevices,
		ConcurrentCaptures: gridSettings.ConcurrentCaptures,
		ActiveInterval:     gridSettings.ActiveCadence,
		IdleInterval:       gridSettings.IdleCadence,
		FreshnessCeiling:   gridSettings.FreshnessCeiling,
	})
	if frameEngineErr != nil {
		log.Printf("frame engine not started: %v", frameEngineErr)
	}
	frameEngineDone := make(chan struct{})
	go func() {
		defer close(frameEngineDone)
		log.Printf("%s", frameEngine.Run(ctx).Report())
	}()

	// The live mirror: one scrcpy session per device, carrying the device's own
	// encoded screen to a viewer and typed input back to it. The engine is built
	// ONCE here and owned by this process: it is cancelled by the same shutdown
	// context as the listener, stopped under a bounded timeout, awaited below
	// before the process returns, and audited at that point, so "no capture
	// outlived this process" is reported from the engine's own numbers rather
	// than assumed. Nothing is subscribed at startup, so no device is captured
	// until an operator opens a mirror on one.
	//
	// A deployment that cannot arm the mirror - no adb path, no scrcpy server,
	// no allow-listed runner - says so in the startup line instead of leaving an
	// operator with a frame that shows nothing and no diagnosis: the same rule
	// that made "no default network profile" a one-look answer.
	mirrorEngine, mirrorErr := mirrorEngineFrom(labService)
	mirrorHost := media.NewMirrorHost(media.MirrorHostConfig{Engine: mirrorEngine, Reason: mirrorErr})
	log.Printf("%s", mirrorHost.State())
	mirrorDone := make(chan struct{})
	go func() {
		defer close(mirrorDone)
		log.Printf("%s", mirrorHost.Run(ctx).Report())
	}()

	routes := []service.Route{
		service.LabAdapterRoute(labService, labToken),
	}
	if artifactAPI != nil {
		routes = append(routes, service.ArtifactRoute(artifactAPI, labToken))
	}
	routes = append(routes, service.ProductRoutes(productHandlers, labToken)...)
	// The text reference registry is the boundary that owns a typed-text value
	// until it is released at dispatch, and the registration surface below is
	// where such a value enters the process. The registry is constructed before
	// the input surface because the dispatcher is given it as the typed-text
	// resolver: typed text is only dispatchable if something can register a
	// reference for it to release (ARC-107).
	textReferences, referenceErr := execution.NewTextReferenceRegistry(clock.System{}, execution.DefaultTextReferenceTTL, execution.DefaultTextReferenceCapacity)
	if referenceErr != nil {
		log.Printf("text reference surface not mounted: %v", referenceErr)
	}

	// The typed device input dispatch path, built ONCE and shared by the input
	// surface and the fleet device-settings surface: one dispatcher owns one
	// serialized actor per device, so a settings apply and a tap for the same
	// device cannot interleave. A surface is mounted only when its whole path was
	// constructed: an empty Route mounts nothing, so a missing dependency degrades
	// to "no surface" rather than to a route that cannot dispatch anything
	// (AGENTS.md section 6).
	// The two artifact ports a catalogued file operation needs, over the
	// workspace's own artifact service: an import and an install read stored
	// bytes, and an export writes the bytes it pulled. It is bound only when a
	// service was constructed, and a file operation refuses rather than acting
	// without its source or its destination.
	fileArtifacts := transportconnect.NewDeviceArtifactSource(artifactService)
	// The host directory a push materializes its payload under. It is resolved
	// once, here, and its absence is stated rather than discovered by the first
	// file operation: a deployment with no admissible transfer directory has
	// file operations it cannot complete, and that is a startup fact.
	transferRoot, transferErr := deviceTransferRoot()
	if transferErr != nil {
		log.Printf("device file operations will refuse: %v", transferErr)
	} else {
		log.Printf("device file transfer directory resolved at %s", transferRoot)
	}
	dispatcher, dispatcherErr := deviceInputDispatcher(labService, actionRuntime, textReferences, db, fileArtifacts, transferRoot)
	if dispatcherErr != nil {
		log.Printf("device dispatch path not constructed: %v", dispatcherErr)
	}
	// The follower fan-out: one operator gesture on the SOURCE frame, carried to
	// each follower the operator selected as its own action through the same
	// kernel. It is built ONCE here and owned by this process - the executor is
	// cancelled by the same shutdown context as the listener and awaited below -
	// because its whole ordering decision is that the followers proceed
	// INDEPENDENTLY of the operator's gesture: a gesture on the source cannot wait
	// for the slowest follower, so the followers' own work has to live somewhere
	// that outlives the request that asked for it.
	//
	// A deployment that cannot build it mounts the input surface without it: a
	// request that names followers is then REFUSED rather than answered with
	// followers that received nothing.
	fanout, fanoutErr := followerFanout(dispatcher, db)
	if fanoutErr != nil {
		log.Printf("follower fan-out not carried: %v", fanoutErr)
	}
	fanoutExecutor, executorErr := followerFanoutExecutor(fanout, db)
	if executorErr != nil {
		log.Printf("follower fan-out not carried: %v", executorErr)
	}
	fanoutDone := make(chan struct{})
	if fanoutExecutor != nil {
		go func() {
			defer close(fanoutDone)
			log.Printf("%s", fanoutExecutor.Run(ctx).Report())
		}()
	} else {
		close(fanoutDone)
	}
	inputMounted := false
	if inputRoute := deviceInputRoute(dispatcher, actionRuntime, fanout, labToken); inputRoute.Path != "" {
		routes = append(routes, inputRoute)
		inputMounted = true
	}
	// The fleet device-settings surface the Console Settings dialog applies
	// from: rotation lock and autofill off, applied across the registry's fleet
	// through the same kernel, one lease per device.
	settingsMounted := false
	if settingsRoute := deviceSettingsRoute(dispatcher, db, labToken); settingsRoute.Path != "" {
		routes = append(routes, settingsRoute)
		settingsMounted = true
	}
	// The big-frame control panel's per-device commands: reboot, switch
	// keyboard, a package install, a file import and a file export, each on the
	// SELECTED device, plus the advanced form's operator-confirmed argument
	// array. It is mounted only when the whole path was constructed, so a
	// deployment that cannot run these operations mounts no surface rather than
	// one that answers every command with a refusal.
	operationsMounted := false
	if operationsRoute := deviceOperationsRoute(dispatcher, db, fileArtifacts, labToken); operationsRoute.Path != "" {
		routes = append(routes, operationsRoute)
		operationsMounted = true
	}
	// The one boundary that admits operator-supplied content into this process,
	// mounted only when a registry was constructed to hold what it admits.
	referenceMounted := false
	if referenceRoute := service.TextReferenceRoute(textReferences, labToken); referenceRoute.Path != "" {
		routes = append(routes, referenceRoute)
		referenceMounted = true
	}
	// The transport surface the OTG Setup tab performs: connect, change a
	// device's transport mode, activate an observed port, restart the adb server.
	// It is mounted only when the whole path was constructed. An empty Route
	// mounts nothing, and a route whose connector cannot reach the adb server is
	// worse than no route at all, because it advertises a surface that cannot
	// work (AGENTS.md section 6). Each reason is logged rather than swallowed, so
	// a deployment that expected the surface can see which dependency was missing.
	connectionMounted := false
	acceptedPorts, acceptedErr := acceptedProfilePorts(context.Background(), store.NewNetworkProfileRepository(db), workspaceID)
	if acceptedErr != nil {
		log.Printf("transport surface not mounted: %v", acceptedErr)
	} else if operations, operationsErr := connectionOperations(labService, acceptedPorts, currentEndpointSource(db, workspaceID)); operationsErr != nil {
		log.Printf("transport surface not mounted: %v", operationsErr)
	} else if connectionRoute := service.ConnectionRoute(operations, labToken); connectionRoute.Path != "" {
		routes = append(routes, connectionRoute)
		connectionMounted = true
	}
	// The live mirror's transport: one WebRTC peer per browser over the engine
	// above, built only when the engine was. A transport with nothing to carry
	// would mount a surface whose first request fails, which is worse than no
	// surface at all. It is owned by this process and closed below, after the
	// engine has stopped, so no peer and no capture outlives the process.
	mirrorStreams, streamsErr := mirrorStreamTransport(mirrorEngine, mirrorErr)
	if streamsErr != nil {
		log.Printf("live mirror transport not started: %v", streamsErr)
	}
	mirrorMounted := false
	if mirrorRoute := deviceMirrorRoute(mirrorStreams, mirrorEngine, actionRuntime, store.NewMirrorEventService(db), labToken); mirrorRoute.Path != "" {
		routes = append(routes, mirrorRoute)
		mirrorMounted = true
	}
	// The per-device stream endpoint the TCP transport is fetched from. It is
	// mounted with the same gate as the surface above and says so in the startup
	// line, because a transport whose endpoint is missing is a transport that
	// cannot work: an operator selecting it would get a frame that never fills.
	streamEndpointMounted := false
	if streamRoute := service.MirrorStreamRoute(mirrorStreamPort{transport: mirrorStreams}, labToken); streamRoute.Path != "" {
		routes = append(routes, streamRoute)
		streamEndpointMounted = true
	}
	log.Printf("live mirror stream endpoint %s at %s (a browser is given a per-device path on this service's own guarded surface; the loopback bind and the token check above are what stand in front of it)", mountState(streamEndpointMounted), transportconnect.MirrorStreamPath)
	// The fleet grid's still previews: the surface that carries a still for EVERY
	// device without spending a device session, so the operator's own frame keeps
	// the one live session it needs. It is mounted only when the capture set was
	// built AND the deployment's grid configuration was readable: a grid whose
	// cadence and level nobody stated is a grid whose tiles behave inexplicably,
	// and no surface is better than one that cannot say what it is carrying.
	gridMounted := false
	if gridRoute := gridPreviewRoute(frameEngine, frameEngineErr, gridSettingsErr, actionRuntime, labToken); gridRoute.Path != "" {
		routes = append(routes, gridRoute)
		gridMounted = true
	}
	server := service.NewHTTPServer("control-plane", address, routes...)
	log.Printf("control-plane listening on %s with lab adapter in %s mode (lab token %s, device input surface %s, device settings surface %s, device operations surface %s, text reference surface %s, transport surface %s, live mirror surface %s, grid preview surface %s)", address, labMode, tokenState(labToken), mountState(inputMounted), mountState(settingsMounted), operationsMountState(operationsMounted), referenceMountState(referenceMounted), connectionMountState(connectionMounted), mirrorMountState(mirrorMounted), mountState(gridMounted))
	serveErr := service.Serve(ctx, server)
	// The startup scan, the post-launch watcher, the mirror-session sweep, the
	// frame engine and the live mirror are owned work, not detached workers: wait
	// for all five to obey cancellation before the process returns. The mirror's
	// own line reports what it started and stopped, so this wait is also where "no
	// capture outlived this process" is answered.
	<-autoScanDone
	<-mirrorSweepDone
	<-transportWatchDone
	<-frameEngineDone
	<-mirrorDone
	// The follower fan-out is owned work too: wait for the runs it accepted to
	// finish, bounded, so no follower's action outlived this plane and every one
	// of them left a row. The wait is the executor's own lifetime, and its line
	// says what it stopped with.
	<-fanoutDone
	// The peers are closed after the engine has stopped, and the wait is bounded:
	// every browser it was carrying is released, and nothing this process opened
	// is left running when it returns.
	if mirrorStreams != nil {
		peerCtx, peerCancel := context.WithTimeout(context.Background(), media.DefaultMirrorCloseTimeout)
		if closeErr := mirrorStreams.Close(peerCtx); closeErr != nil {
			log.Printf("live mirror transport stopped with work outstanding: %v", closeErr)
		} else {
			log.Print("live mirror transport stopped: no browser stream outlived this process")
		}
		peerCancel()
	}
	if serveErr != nil {
		log.Fatal(serveErr)
	}
}

// mirrorStreamTransport builds the live mirror's WebRTC transport over the
// engine, or reports why it cannot.
//
// A transport is only built when the engine was: it carries that engine's
// sessions, and a transport over an engine that could not be built would mount a
// surface whose first request fails. Its peers are owned by the caller - the
// process - which closes them at shutdown.
func mirrorStreamTransport(engine *media.MirrorEngine, engineErr error) (*media.StreamTransport, error) {
	if engineErr != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeUnavailable, "the live mirror has no engine to carry streams from", engineErr)
	}
	if engine == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the live mirror has no engine to carry streams from")
	}
	return media.NewStreamTransport(media.StreamTransportConfig{Mirror: engine})
}

// deviceMirrorRoute builds the live mirror route, or an empty Route when the
// transport or the device-to-serial resolver is missing.
//
// The resolver is the same registry the input surface resolves devices through:
// one vocabulary decides which transport a device is currently reachable at, and
// the browser never names or receives one.
func deviceMirrorRoute(streams *media.StreamTransport, engine *media.MirrorEngine, resolver transportconnect.DeviceSerialResolver, refusals transportconnect.MirrorRefusalRecorder, token string) service.Route {
	if streams == nil {
		log.Print("live mirror surface not mounted: no stream transport was constructed")
		return service.Route{}
	}
	return service.DeviceMirrorRoute(mirrorStreamPort{transport: streams}, resolver, engine, refusals, token)
}

// gridPreviewRoute builds the fleet grid's still surface, or an empty Route when
// the capture set was not built or the deployment's grid configuration could not
// be read.
//
// The resolver is the same registry the input surface resolves devices through:
// one vocabulary decides which transport a device is currently reachable at, and
// a console names a device rather than a transport.
//
// Nothing here is derived: the engine is the plane's one owned capture worker, so
// the cadence, the level and the sweep bound the grid is served at are the ones
// that worker is running with.
func gridPreviewRoute(engine *media.FrameEngine, engineErr error, settingsErr error, resolver transportconnect.DeviceSerialResolver, token string) service.Route {
	if engineErr != nil {
		log.Printf("grid preview surface not mounted: the capture set was not built: %v", engineErr)
		return service.Route{}
	}
	if settingsErr != nil {
		return service.Route{}
	}
	if engine == nil {
		log.Print("grid preview surface not mounted: no capture set was constructed")
		return service.Route{}
	}
	return service.GridPreviewRoute(engine, resolver, token)
}

// mirrorStreamPort adapts the media transport to the surface's own port: the
// transport hands back its peer type, and the port takes the narrow shape the
// handler needs. Nothing is derived here and nothing is wrapped twice.
type mirrorStreamPort struct{ transport *media.StreamTransport }

func (p mirrorStreamPort) Open(ctx context.Context, deviceID, serial string, transport media.MirrorTransportKind, purpose media.MirrorViewerPurpose, preview media.MirrorPreview) (transportconnect.DeviceMirrorStream, error) {
	carrier, err := p.transport.Open(ctx, deviceID, serial, transport, purpose, preview)
	if err != nil {
		return nil, err
	}
	return carrier, nil
}

func (p mirrorStreamPort) Stream(streamKey string) (transportconnect.DeviceMirrorStream, bool) {
	carrier, live := p.transport.Stream(streamKey)
	if !live {
		return nil, false
	}
	return carrier, true
}

// Carrying reports the identities of the streams this transport is carrying, which
// is what the surface's "no live stream with that identity" refusal names beside
// the identity it was given. Only the stream key crosses this seam: the stats the
// transport reports also carry the serial each stream came from, and that is never
// part of what a browser-facing refusal states (AGENTS.md section 9).
func (p mirrorStreamPort) Carrying() []string {
	peers := p.transport.Peers()
	keys := make([]string, 0, len(peers))
	for _, peer := range peers {
		keys = append(keys, peer.StreamKey)
	}
	return keys
}

// mirrorMountState reports whether the live mirror surface was mounted. Like the
// other state reporters it says nothing about whether a mounted surface will
// carry a stream: opening one is a request, and the engine decides.
func mirrorMountState(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "not mounted; no live mirror transport was constructed"
}

// mirrorEngineFrom builds the live mirror engine over the device's own
// allow-listed runner, or reports why it cannot be built.
//
// It is the deployment seam for the mirror. Its inputs are the deployment's own
// configuration - the same adb the lab service reaches devices through, plus the
// host path of the scrcpy server that is pushed to each device - and the engine
// is constructed here, once, rather than lazily on the first viewer's request,
// so a deployment that cannot mirror says why at startup instead of showing an
// operator a frame with nothing in it.
//
// The runner handed to the dialer is the lab service's own device transport, not
// a second adapter: the mirror's push, tunnel and launch therefore pass the same
// allow-list admission every other device command in this process passes. The
// engine it returns is owned by the caller - the process - and the composition's
// MirrorHost is what stops it.
func mirrorEngineFrom(labService *lab.Service) (*media.MirrorEngine, error) {
	dialer, err := mirror.NewDialerFromEnv(os.LookupEnv, labService.DeviceTransport())
	if err != nil {
		return nil, err
	}
	// The bound is decided HERE and nowhere else. It is the plane's own
	// device-session capacity, stated at composition from the deployment's
	// configuration: the console's grid no longer keeps a copy of the number, it
	// is told this one (see the capacity surface), so the two surfaces cannot
	// disagree. `MaxSessions` and `OperatorReserve` are passed explicitly even when
	// the deployment configured neither, so the number the engine carries is always
	// a number this line stated.
	capacity, reserve, capacityErr := media.SessionCapacityFromEnv(os.LookupEnv)
	if capacityErr != nil {
		return nil, capacityErr
	}
	// The workspace's preview setting is decided here too, and for the same
	// reason: it is a BOUND the plane applies to every ambient stream, so a
	// deployment that mistyped it must be a startup diagnosis rather than a grid
	// of tiles behaving inexplicably. It is passed explicitly even when the
	// deployment configured nothing, so the setting the engine carries is always
	// a setting and never "no bound".
	//
	// It is also the PROFILE the plane's live-stream budget is spent at: a stream
	// at this level is what the transport carries, so what one of them costs is
	// read from this one input rather than stated a second time beside it.
	preview, previewErr := media.PreviewFromEnv(os.LookupEnv)
	if previewErr != nil {
		return nil, previewErr
	}
	// How much of the transport this deployment has measured is the other half of
	// that bound, and it is an input for the same reason: a plane that invented a
	// budget would size a grid against a path nobody measured.
	budgetKbps, budgetErr := media.TransportBudgetFromEnv(os.LookupEnv)
	if budgetErr != nil {
		return nil, budgetErr
	}
	return media.NewMirrorEngine(media.MirrorEngineConfig{
		Dialer:              dialer,
		MaxSessions:         capacity,
		OperatorReserve:     reserve,
		Preview:             preview,
		TransportBudgetKbps: budgetKbps,
	})
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

// referenceMountState reports whether the registration surface was mounted, and
// separately from the input surface, because the two fail for different reasons:
// a registry that could not be constructed is not a dispatcher that was not.
func referenceMountState(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "not mounted; no reference registry was constructed"
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

// deviceInputDispatcher builds the typed device input dispatcher, or reports why
// it cannot be built.
//
// It is built ONCE and shared by every surface that dispatches a device action,
// because the dispatcher owns one serialized actor per device: two dispatchers
// would be two actors for one device, and the per-device serialization the actor
// exists to provide would be defeated by having two of them.
func deviceInputDispatcher(labService *lab.Service, resolver *execution.Registry, textReferences *execution.TextReferenceRegistry, db *store.DB, artifactSource *transportconnect.DeviceArtifactSource, transferRoot string) (*execution.InputDispatcher, error) {
	transport, err := execution.NewInputTransportFromAllowlisted(labService.DeviceTransport())
	if err != nil {
		return nil, err
	}
	observer, err := execution.NewObservationPostconditionObserver(resolver, observationSource(labService))
	if err != nil {
		return nil, err
	}
	deviceState, err := execution.NewTransportObserverFromAttached(labService)
	if err != nil {
		return nil, err
	}
	// The departure port a reboot's postcondition is read through, over the same
	// narrow transport every other device call travels. It is constructed here
	// rather than defaulted inside the dispatcher, because a dispatcher built
	// without one must REFUSE a reboot rather than restart a device whose return
	// it could never observe.
	departures, err := execution.NewTransportDepartureObserver(transport, 0)
	if err != nil {
		return nil, err
	}
	options := []execution.DispatcherOption{
		execution.WithEvidenceRecorder(store.NewActionEvidenceService(db)),
		execution.WithOperationDepartureObserver(departures),
	}
	if strings.TrimSpace(transferRoot) != "" {
		options = append(options, execution.WithOperationTransferRoot(transferRoot))
	}
	if artifactSource != nil {
		// The store an export writes the bytes it pulled into, and the source an
		// import and an install read theirs from. Bound as one object, because
		// the two directions are the same storage seen from two sides.
		options = append(options, execution.WithOperationFileArchive(artifactSource))
	}
	return execution.NewInputDispatcher(
		store.NewActionService(db),
		execution.NewStoreControlProbe(db, deviceState),
		observer,
		transport,
		// The typed-text resolver: the same registry the registration surface
		// writes to, so a typed-text payload releases a value only if one was
		// registered for its workspace. With no registry (no surface was
		// constructed) the primitive still fails closed on its own.
		textReferences,
		options...,
	)
}

// deviceTransferRoot resolves the host directory a catalogued device file
// operation materializes a push under, or reports why no admissible one exists.
//
// The directory is a DEPLOYMENT input, because the composed transfer path has to
// satisfy the device adapter's own allow-list - a path with no whitespace and no
// character that carries meaning to a shell - and only the deployment knows where
// its data lives. The configured value is checked against that same rule at
// startup with `adb.ValidateTransferRoot`, so a root the adapter would refuse is
// reported here rather than at the first file operation.
//
// A deployment that names none gets the platform's own temporary directory: a
// transfer file exists only between the push and the device's acknowledgement, so
// a transient location is what it is for. Where even that is inadmissible the
// caller binds no root at all, and a file operation then refuses with its own
// reason rather than writing into a directory nobody chose.
func deviceTransferRoot() (string, error) {
	configured := strings.TrimSpace(os.Getenv(envTransferRoot))
	if configured != "" {
		if !filepath.IsAbs(configured) {
			return "", platformerrors.New(platformerrors.CodeInvalidInput, envTransferRoot+" must be an absolute path")
		}
		if err := adb.ValidateTransferRoot(configured); err != nil {
			return "", platformerrors.Wrap(platformerrors.CodeInvalidInput, envTransferRoot+" is not a directory this product's device adapter admits", err)
		}
		return configured, nil
	}
	for _, candidate := range []string{
		filepath.Join(os.TempDir(), "drift-next-transfer"),
		filepath.Join(filepath.Dir(product.DefaultControlPlaneDBPath()), "drift-next-transfer"),
	} {
		if adb.ValidateTransferRoot(candidate) == nil {
			return candidate, nil
		}
	}
	return "", platformerrors.New(platformerrors.CodeUnavailable, "no admissible transfer directory could be resolved, so device file operations cannot materialize a payload")
}

// deviceInputRoute builds the typed device input route, or an empty Route when the
// path cannot be constructed.
//
// The composed path, in the order a request travels it: the transport is the
// device's own allow-listed runner (4a); the readiness probe judges the device from
// the lab boundary's attached set (4b); the postcondition observer reads the
// device's current observation (slice 3); the dispatcher carries the whole P7
// contract with the evidence recorder bound; and the application boundary resolves
// the device to its serial and assigns the attempt identity (4c), which is what
// satisfies the port this route serves.
func deviceInputRoute(dispatcher *execution.InputDispatcher, resolver *execution.Registry, fanout *execution.FollowerFanout, token string) service.Route {
	if dispatcher == nil {
		log.Print("device input surface not mounted: the dispatcher was not constructed")
		return service.Route{}
	}
	options := []transportconnect.DeviceInputBoundaryOption{}
	if fanout != nil {
		options = append(options, transportconnect.WithFollowerFanout(fanout))
	} else {
		log.Print("device input surface mounted without the follower fan-out: a request that names followers will be refused")
	}
	boundary, err := transportconnect.NewDeviceInputBoundary(dispatcher, resolver, ids.NewRandom(), options...)
	if err != nil {
		log.Printf("device input surface not mounted: %v", err)
		return service.Route{}
	}
	return service.DeviceInputRoute(boundary, token)
}

// followerFanout builds the fan-out an operator's gesture is carried to the
// selected followers through, or reports why it cannot be built.
//
// Every part is the SAME part the source's own input travels: the fleet reader that
// makes the one ONLINE reading, the dispatcher that owns one serialized actor per
// device, and the store's own control services. A fan-out that resolved its own
// candidates or dispatched beside the kernel would be a second, unaudited place
// where authority is decided.
func followerFanout(dispatcher *execution.InputDispatcher, db *store.DB) (*execution.FollowerFanout, error) {
	if dispatcher == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the follower fan-out requires the device input dispatcher, which was not constructed")
	}
	if db == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the follower fan-out requires the store")
	}
	fleet := execution.NewStoreFleetReader(db)
	control := product.NewFollowerInputControl(db)
	if control == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the follower fan-out requires the plane's control services")
	}
	return execution.NewFollowerFanout(fleet, control, dispatcher, ids.NewRandom())
}

// followerFanoutExecutor builds the bounded, owned worker the fan-out's runs are
// handed to, and binds it back to the fan-out it runs work through.
//
// The two halves need each other - the fan-out accepts the work and the executor
// runs it, and the executor runs work through the fan-out - so the binding is
// explicit and one-directional here rather than a cycle the constructors resolve.
func followerFanoutExecutor(fanout *execution.FollowerFanout, db *store.DB) (*execution.FollowerFanoutExecutor, error) {
	if fanout == nil || db == nil {
		return nil, platformerrors.New(platformerrors.CodeUnavailable, "the follower fan-out executor requires the fan-out and the store")
	}
	executor, err := execution.NewFollowerFanoutExecutor(execution.FollowerFanoutConfig{
		Runner: fanout,
		Sink:   product.NewFollowerInputOutcomeSink(db),
	})
	if err != nil {
		return nil, err
	}
	if err := fanout.BindRunStarter(executor); err != nil {
		return nil, err
	}
	return executor, nil
}

// deviceSettingsRoute builds the fleet device-settings route, or an empty Route
// when the path cannot be constructed.
//
// It shares the ONE dispatcher the input surface dispatches through, so a
// settings apply and a tap for the same device reach that device through the same
// serialized actor and cannot interleave. Every dependency is checked and every
// absence returns an empty Route, which mounts nothing at all: a route whose
// applier cannot apply is worse than no route, because it advertises a surface
// that cannot work (AGENTS.md section 6).
func deviceSettingsRoute(dispatcher *execution.InputDispatcher, db *store.DB, token string) service.Route {
	if dispatcher == nil {
		log.Print("device settings surface not mounted: the dispatcher was not constructed")
		return service.Route{}
	}
	applier, err := execution.NewDeviceSettingsApplier(execution.NewStoreFleetReader(db), db, dispatcher, ids.NewRandom())
	if err != nil {
		log.Printf("device settings surface not mounted: %v", err)
		return service.Route{}
	}
	return service.DeviceSettingsRoute(applier, token)
}

// deviceOperationsRoute builds the big-frame control panel's per-device command
// route, or an empty Route when the path cannot be constructed.
//
// It shares the ONE dispatcher every other device surface dispatches through, so
// a reboot and a tap for the same device reach that device through the same
// serialized actor and cannot interleave. The applier is given the same artifact
// ports the dispatcher holds, because they are the same storage seen from the two
// sides of a file operation.
//
// Every absence returns an empty Route, which mounts nothing at all: a route
// whose applier cannot run an operation is worse than no route, because it
// advertises a surface that cannot work (AGENTS.md section 7).
func deviceOperationsRoute(dispatcher *execution.InputDispatcher, db *store.DB, artifacts *transportconnect.DeviceArtifactSource, token string) service.Route {
	if dispatcher == nil {
		log.Print("device operations surface not mounted: the dispatcher was not constructed")
		return service.Route{}
	}
	var reader execution.DeviceArtifactReader
	if artifacts != nil {
		reader = artifacts
	}
	applier, err := execution.NewDeviceOperationsApplier(execution.NewStoreFleetReader(db), db, dispatcher, ids.NewRandom(), reader)
	if err != nil {
		log.Printf("device operations surface not mounted: %v", err)
		return service.Route{}
	}
	return service.DeviceOperationsRoute(applier, token)
}

// operationsMountState reports whether the panel's device-operation surface was
// mounted. Like the other state reporters it says nothing about whether a mounted
// surface will accept an operation: the kernel and the policy evaluator decide
// every dispatch.
func operationsMountState(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "not mounted; the device operations path was not constructed"
}

// acceptedProfilePorts reads the workspace's DEFAULT network profile's declared
// ports: the set a transport may be opened on without an activation. The profile
// is the operator's own declaration, validated when it was written, so the policy
// comes from what the operator said rather than from a constant in this binary,
// and it is the same declaration the scan is bounded by.
//
// A workspace with no default profile yields an error and the transport surface is
// not mounted, because a policy with no accepted set refuses every endpoint and
// would advertise a surface that can only refuse (AGENTS.md section 7).
func acceptedProfilePorts(ctx context.Context, profiles *store.NetworkProfileRepository, workspace organizations.WorkspaceID) ([]uint16, error) {
	listed, err := profiles.List(ctx, workspace)
	if err != nil {
		return nil, err
	}
	for _, profile := range listed {
		if profile.IsDefault {
			return profile.Ports, nil
		}
	}
	return nil, platformerrors.New(platformerrors.CodeInvalidInput, "no default network profile is configured, so no port is accepted for a transport")
}

// currentEndpointSource is the registry read every connect is bounded by: the
// addresses this workspace currently holds as current. It is wired here rather
// than inside the connection package because the workspace is a composition fact -
// the domain is handed the set it may dial and never learns which workspace it came
// from (ARC-231).
func currentEndpointSource(db *store.DB, workspaceID organizations.WorkspaceID) connection.CurrentEndpointSource {
	return connection.CurrentEndpointSourceFunc(func(ctx context.Context) ([]string, error) {
		return store.NewEndpointRepository(db).ListCurrentAddresses(ctx, workspaceID)
	})
}

// connectionOperations builds the transport boundary over the lab service's own
// runners. It is the composition root's job: the service exposes the host and
// device runners it was constructed with, and this assembles them into the single
// boundary the transport surface depends on. Nothing here is derived by type
// assertion on a stored field, because the service's own rule is that exposing a
// runner is an explicit opt-in and never an implicit one.
//
// The enumerator is the lab service itself: it already enumerates attached
// transports as adb-discovered devices, so a restart re-establishes endpoints from
// the same view the rest of the product sees rather than from a second one.
//
// The activator is given no Wait: NewActivator fills the production default, which
// bounds the settle after tcpip against the caller's context rather than sleeping
// through a cancellation. Only a test injects one.
//
// The connector and the restarter are both given the registry's current endpoints,
// so an address this plane does not currently observe is refused before any device
// call whichever surface asked for it (ARC-231).
func connectionOperations(labService *lab.Service, acceptedPorts []uint16, current connection.CurrentEndpointSource) (transportconnect.ConnectionOperations, error) {
	host := labService.HostTransport()
	device := labService.DeviceTransport()
	enumerator := labService.Enumerator()
	if host == nil || device == nil || enumerator == nil {
		return transportconnect.ConnectionOperations{}, platformerrors.New(platformerrors.CodeUnavailable, "the lab service exposes no transport runner, so no transport operation can reach a device")
	}
	policy := connection.NewPortPolicy(acceptedPorts)
	// Activation is the operator's decision, recorded per device. It lives in
	// process memory because nothing durable is required for it yet: a restart
	// forgets it, which is fail-closed rather than permissive.
	activations := connection.NewPortActivations()
	connector, connectorErr := connection.NewConnector(connection.ConnectorConfig{Runner: host, Policy: policy, Activations: activations, Current: current})
	if connectorErr != nil {
		return transportconnect.ConnectionOperations{}, connectorErr
	}
	restarter, restarterErr := connection.NewRestarter(connection.RestarterConfig{Runner: host, Enumerator: enumerator, Policy: policy, Current: current})
	if restarterErr != nil {
		return transportconnect.ConnectionOperations{}, restarterErr
	}
	activator, activatorErr := connection.NewActivator(connection.ActivatorConfig{Runner: device, Enumerator: enumerator})
	if activatorErr != nil {
		return transportconnect.ConnectionOperations{}, activatorErr
	}
	return transportconnect.ConnectionOperations{Connector: connector, Restarter: restarter, Activator: activator}, nil
}

// connectionMountState reports whether the transport surface was mounted. Like the
// other state reporters it says nothing about whether a mounted surface will accept
// an action: the port policy and the kernel still decide every operation.
func connectionMountState(mounted bool) string {
	if mounted {
		return "mounted"
	}
	return "not mounted; the transport path was not constructed"
}
