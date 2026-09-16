package runtime

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type componentState string

const (
	stateStopped  componentState = "stopped"
	stateStarting componentState = "starting"
	stateReady    componentState = "ready"
	stateFailed   componentState = "failed"
	// stateExternal means an address is served by a process this session did not
	// start. It is deliberately not stateReady: a responding port is not health.
	stateExternal componentState = "external"
)

// ComponentStatus is safe to render in a terminal or desktop status surface.
type ComponentStatus struct {
	Name   string
	State  componentState
	Detail string
}

type childProcess interface {
	Stop() error
}

type processStarter func(context.Context, string, []string, []string, io.Writer) (childProcess, error)

// listener is one process bound to an address, as far as the platform can tell.
// PID and command are used to name a holder for the operator, and the pid is
// used only to signal a process the operator explicitly chose to terminate.
type listener struct {
	PID     int
	Command string
}

// portChecker answers whether a local TCP address can still be bound by this
// process, names whatever holds it when it cannot, and lists the holders when
// the operator has to decide what to do about one. It is a seam so tests never
// bind a real port and never signal a real process.
type portChecker interface {
	Free(address string) bool
	Holder(address string) string
	Listeners(address string) []listener
}

// readyProbe reports whether something currently answers the readiness endpoint
// for an address. It is a seam so tests never make real HTTP requests.
type readyProbe func(context.Context, string) bool

// managedComponent is one allow-listed service the supervisor can own.
type managedComponent struct {
	Name    string
	Address func(Config) string
}

// managedComponents is the single source of truth for component names, their
// configured address, and the order status is rendered in.
var managedComponents = []managedComponent{
	{Name: "Control Plane", Address: func(c Config) string { return c.ControlPlaneAddress }},
	{Name: "Device Service", Address: func(c Config) string { return c.EdgeAgentAddress }},
	{Name: "Desktop Application", Address: func(Config) string { return "" }},
}

const (
	defaultFreeWindow   = 2 * time.Second
	defaultPollInterval = 100 * time.Millisecond
	defaultReadyTimeout = 15 * time.Second
)

// Supervisor owns only the allow-listed local processes needed by Drift.
type Supervisor struct {
	config      Config
	dataDir     string
	start       processStarter
	client      *http.Client
	ports       portChecker
	probe       readyProbe
	discoverADB func(string) (string, error)
	runCommand  func(context.Context, string, ...string) error
	freeWindow  time.Duration
	poll        time.Duration
	readyWait   time.Duration
	mu          sync.Mutex
	processes   map[string]childProcess
	statuses    map[string]ComponentStatus
	logs        []string
	eventSink   func(string)
	// terminate asks exactly one process to stop, for a listener an operator
	// explicitly chose to terminate. It is a seam so no test signals a real
	// process, and it is the only place this runtime signals a process it did
	// not start.
	terminate func(int) error
	// adopted records the listener an operator chose to keep, per component
	// name. It is a record of a decision the operator made, never an inference
	// this runtime drew on their behalf.
	adopted map[string]adoptedListener
}

func newSupervisor(config Config, dataDir string) *Supervisor {
	supervisor := &Supervisor{
		config:      config,
		dataDir:     dataDir,
		start:       startCommand,
		client:      &http.Client{Timeout: 750 * time.Millisecond},
		ports:       netPortChecker{},
		discoverADB: DefaultADBDiscovery,
		runCommand:  runCommand,
		freeWindow:  defaultFreeWindow,
		poll:        defaultPollInterval,
		readyWait:   defaultReadyTimeout,
		processes:   map[string]childProcess{},
		statuses:    map[string]ComponentStatus{},
		terminate:   terminateProcess,
		adopted:     map[string]adoptedListener{},
	}
	supervisor.probe = supervisor.probeReady
	return supervisor
}

func NewSupervisor(config Config, dataDir string) *Supervisor { return newSupervisor(config, dataDir) }

func (s *Supervisor) setStatus(name string, state componentState, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[name] = ComponentStatus{Name: name, State: state, Detail: detail}
}

// Status returns the last observed state of every allow-listed component. A
// component with no observation is reported as stopped, never as ready.
func (s *Supervisor) Status() []ComponentStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]ComponentStatus, 0, len(managedComponents))
	for _, component := range managedComponents {
		status := s.statuses[component.Name]
		if status.Name == "" {
			status = ComponentStatus{Name: component.Name, State: stateStopped, Detail: "Not started"}
		}
		result = append(result, status)
	}
	return result
}
func (s *Supervisor) Logs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logs...)
}

// SetEventSink receives redacted lifecycle and process-output events for an
// interactive operator surface.
func (s *Supervisor) SetEventSink(sink func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventSink = sink
}

// componentAddress resolves the configured address for a component. It is empty
// for components that do not expose a local TCP address.
func (s *Supervisor) componentAddress(name string) string {
	for _, component := range managedComponents {
		if component.Name == name {
			return component.Address(s.config)
		}
	}
	return ""
}

// owns reports whether this session started the component's process.
func (s *Supervisor) owns(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.processes[name]
	return ok
}

// observedState returns the last recorded state for a component.
func (s *Supervisor) observedState(name string) componentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statuses[name].State
}

// RefreshStatus observes already-running local services without starting or
// stopping anything. This keeps a restarted TUI honest about runtime state.
//
// A response on a component's port is not health: any process can answer there.
// A component is only reported ready when this session started its process. A
// listener this session does not own is reported as external, and one an
// operator chose to keep is reported as adopted - never as ready, because
// nothing verified what that process is running.
func (s *Supervisor) RefreshStatus(ctx context.Context) {
	for _, component := range managedComponents {
		address := component.Address(s.config)
		if address == "" {
			continue
		}
		if s.owns(component.Name) {
			if s.probe(ctx, address) {
				s.setStatus(component.Name, stateReady, "Ready (started by this session)")
				continue
			}
			s.setStatus(component.Name, stateStarting, "Started by this session; not answering /readyz yet")
			continue
		}
		if entry, adopted := s.adoptedListenerFor(component.Name); adopted {
			detail := adoptedDetail(entry)
			if s.observedState(component.Name) != stateAdopted {
				s.appendLog(component.Name + ": " + detail)
			}
			s.setStatus(component.Name, stateAdopted, detail)
			continue
		}
		if s.probe(ctx, address) {
			detail := externalDetail(address, s.describeHolder(address))
			if s.observedState(component.Name) != stateExternal {
				s.appendLog(component.Name + ": " + detail)
			}
			s.setStatus(component.Name, stateExternal, detail)
			continue
		}
		s.setStatus(component.Name, stateStopped, "Not started")
	}
}

// probeReady performs the readiness request used by waitReady.
func (s *Supervisor) probeReady(ctx context.Context, address string) bool {
	if strings.TrimSpace(address) == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/readyz", nil)
	if err != nil {
		return false
	}
	response, err := s.client.Do(req)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// RunChecks executes the repository's fixed validation commands without a shell.
func (s *Supervisor) RunChecks(ctx context.Context) error {
	commands := [][]string{{"pnpm", "typecheck"}, {"pnpm", "lint"}, {"pnpm", "test", "--", "--reporter=dot"}, {"pnpm", "build"}, {"go", "test", "./..."}, {"go", "vet", "./..."}, {"go", "build", "./..."}, {"buf", "lint"}, {"buf", "build"}, {"bash", "scripts/secret-scan.sh"}, {"git", "diff", "--check"}}
	return s.runFixedCommands(ctx, commands)
}

// BuildAll builds the desktop application without creating a distributable bundle.
func (s *Supervisor) BuildAll(ctx context.Context) error {
	return s.runFixedCommands(ctx, [][]string{{"pnpm", "--filter", "console", "exec", "tauri", "build", "--debug", "--no-bundle"}})
}

// RunRealDeviceTests runs only the repository's explicit real-device test target.
func (s *Supervisor) RunRealDeviceTests(ctx context.Context) error {
	return s.runFixedCommands(ctx, [][]string{{"go", "test", "./internal/edge/lab", "-run", "RealDevice"}})
}

func (s *Supervisor) runFixedCommands(ctx context.Context, commands [][]string) error {
	for _, command := range commands {
		path, err := exec.LookPath(command[0])
		if err != nil {
			return fmt.Errorf("%s is unavailable: %w", command[0], err)
		}
		s.appendLog("Running " + strings.Join(command, " "))
		process := exec.CommandContext(ctx, path, command[1:]...)
		process.Dir = repoRoot()
		process.Env = os.Environ()
		process.Stdout = logWriter{supervisor: s}
		process.Stderr = logWriter{supervisor: s}
		if err := process.Run(); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(command, " "), err)
		}
	}
	return nil
}

func repoRoot() string {
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return "."
}

func (s *Supervisor) appendLog(line string) {
	s.mu.Lock()
	line = strings.TrimSpace(line)
	if line == "" {
		s.mu.Unlock()
		return
	}
	line = strings.ReplaceAll(line, s.config.ServiceToken, "[REDACTED]")
	s.logs = append(s.logs, line)
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
	sink := s.eventSink
	s.mu.Unlock()
	if sink != nil {
		sink(line)
	}
}

// Setup validates local prerequisites and creates the owned storage/configuration.
func (s *Supervisor) Setup(ctx context.Context) error {
	adbPath, err := s.discoverADB(s.config.ADBPath)
	if err != nil {
		return err
	}
	s.config.ADBPath = adbPath
	if err := os.MkdirAll(filepath.Dir(s.config.DatabasePath), 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	if err := os.MkdirAll(s.config.ArtifactRoot, 0o700); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	if err := s.runCommand(ctx, adbPath, "start-server"); err != nil {
		return fmt.Errorf("start adb: %w", err)
	}
	s.appendLog("ADB discovered and validated")
	return nil
}

// StartAll starts the services in dependency order and waits for readiness.
func (s *Supervisor) StartAll(ctx context.Context, launchDesktop bool) error {
	if err := s.Setup(ctx); err != nil {
		return err
	}
	if err := s.startComponent(ctx, "Control Plane", s.config.ControlPlaneAddress, "go", []string{"run", "./cmd/control-plane"}, []string{
		"DRIFT_CONTROL_PLANE_ADDR=" + s.config.ControlPlaneAddress,
		"DRIFT_CONTROL_PLANE_DB=" + s.config.DatabasePath,
		"DRIFT_ARTIFACT_CAS_ROOT=" + s.config.ArtifactRoot,
		"DRIFT_RUNTIME_DEVICE_MODE=connected",
		"DRIFT_RUNTIME_ADB_PATH=" + s.config.ADBPath,
		"DRIFT_RUNTIME_SERVICE_TOKEN=" + s.config.ServiceToken,
	}); err != nil {
		return err
	}
	if err := s.waitReadyFor(ctx, "Control Plane", s.config.ControlPlaneAddress); err != nil {
		_ = s.StopAll(ctx)
		return fmt.Errorf("control plane readiness: %w", err)
	}
	if err := s.startComponent(ctx, "Device Service", s.config.EdgeAgentAddress, "go", []string{"run", "./cmd/edge-agent"}, []string{"DRIFT_EDGE_AGENT_ADDR=" + s.config.EdgeAgentAddress}); err != nil {
		_ = s.StopAll(ctx)
		return err
	}
	if err := s.waitReadyFor(ctx, "Device Service", s.config.EdgeAgentAddress); err != nil {
		_ = s.StopAll(ctx)
		return fmt.Errorf("device service readiness: %w", err)
	}
	if launchDesktop {
		if err := s.startComponent(ctx, "Desktop Application", "", "pnpm", []string{"--filter", "console", "exec", "tauri", "dev"}, []string{
			"VITE_DRIFT_CONTROL_PLANE_URL=http://" + s.config.ControlPlaneAddress,
			"VITE_DRIFT_RUNTIME_ADAPTER_URL=http://" + s.config.ControlPlaneAddress,
			"VITE_DRIFT_RUNTIME_SERVICE_TOKEN=" + s.config.ServiceToken,
			"VITE_DRIFT_RUNTIME_OPERATOR_ID=" + s.config.OperatorID,
		}); err != nil {
			_ = s.StopAll(ctx)
			return err
		}
	}
	s.appendLog("All requested components are ready")
	return nil
}

// StartComponent starts one allow-listed component for troubleshooting.
func (s *Supervisor) StartComponent(ctx context.Context, name string) error {
	if err := s.Setup(ctx); err != nil {
		return err
	}
	switch name {
	case "Control Plane":
		if err := s.startComponent(ctx, name, s.config.ControlPlaneAddress, "go", []string{"run", "./cmd/control-plane"}, []string{"DRIFT_CONTROL_PLANE_ADDR=" + s.config.ControlPlaneAddress, "DRIFT_CONTROL_PLANE_DB=" + s.config.DatabasePath, "DRIFT_ARTIFACT_CAS_ROOT=" + s.config.ArtifactRoot, "DRIFT_RUNTIME_DEVICE_MODE=connected", "DRIFT_RUNTIME_ADB_PATH=" + s.config.ADBPath, "DRIFT_RUNTIME_SERVICE_TOKEN=" + s.config.ServiceToken}); err != nil {
			return err
		}
		return s.waitReadyFor(ctx, name, s.config.ControlPlaneAddress)
	case "Device Service":
		if err := s.startComponent(ctx, name, s.config.EdgeAgentAddress, "go", []string{"run", "./cmd/edge-agent"}, []string{"DRIFT_EDGE_AGENT_ADDR=" + s.config.EdgeAgentAddress}); err != nil {
			return err
		}
		return s.waitReady(ctx, name, s.config.EdgeAgentAddress)
	case "Desktop Application":
		return s.startComponent(ctx, name, "", "pnpm", []string{"--filter", "console", "exec", "tauri", "dev"}, []string{"VITE_DRIFT_CONTROL_PLANE_URL=http://" + s.config.ControlPlaneAddress, "VITE_DRIFT_RUNTIME_ADAPTER_URL=http://" + s.config.ControlPlaneAddress, "VITE_DRIFT_RUNTIME_SERVICE_TOKEN=" + s.config.ServiceToken, "VITE_DRIFT_RUNTIME_OPERATOR_ID=" + s.config.OperatorID})
	default:
		return fmt.Errorf("unknown component %q", name)
	}
}

// StopComponent stops one component this session started and then verifies that
// the address is actually free. A listener this session does not own is a
// failure, not a successful stop: reporting success there tells the operator a
// stale process was replaced when it was not.
//
// An adopted listener is the deliberate exception: the operator chose to keep
// that process, so leaving it running is the outcome they asked for rather than
// a stop that failed.
func (s *Supervisor) StopComponent(name string) error {
	address := s.componentAddress(name)
	s.mu.Lock()
	process := s.processes[name]
	delete(s.processes, name)
	s.mu.Unlock()
	if process == nil {
		if s.addressFree(address) {
			s.setStatus(name, stateStopped, "Stopped (nothing was started by this session)")
			return nil
		}
		if entry, adopted := s.adoptedListenerFor(name); adopted {
			s.setStatus(name, stateAdopted, adoptedDetail(entry))
			return nil
		}
		return s.failOperation(name, fmt.Errorf("%s: nothing was started by this session, but %s is still held by %s; %s, or %s", name, address, s.describeHolder(address), resolveAdoptOffer, resolveTerminateOffer))
	}
	if err := process.Stop(); err != nil {
		s.setStatus(name, stateFailed, fmt.Sprintf("stop failed: %v", err))
		return fmt.Errorf("stop %s: %w", name, err)
	}
	if !s.waitAddressFree(address) {
		return s.failOperation(name, fmt.Errorf("%s: stopped the process this session started, but %s is still held by %s; %s, or %s", name, address, s.describeHolder(address), resolveAdoptOffer, resolveTerminateOffer))
	}
	s.setStatus(name, stateStopped, "Stopped")
	return nil
}

func (s *Supervisor) startComponent(ctx context.Context, name, address, executable string, args, extraEnv []string) error {
	if s.owns(name) {
		s.setStatus(name, stateReady, "Ready (already running in this session)")
		return nil
	}
	// A process this session does not own would keep the address, so the child
	// could not bind and the stale binary would keep serving. Refuse instead -
	// unless the operator already decided to keep that process, in which case
	// there is nothing to start and the component is reported as adopted rather
	// than as anything this session verified.
	if !s.addressFree(address) {
		if entry, adopted := s.adoptedListenerFor(name); adopted {
			s.setStatus(name, stateAdopted, adoptedDetail(entry))
			return nil
		}
		return s.failOperation(name, blockedStartError(name, address, s.describeHolder(address)))
	}
	s.setStatus(name, stateStarting, "Starting")
	process, err := s.start(ctx, executable, args, extraEnv, logWriter{supervisor: s})
	if err != nil {
		s.setStatus(name, stateFailed, err.Error())
		return fmt.Errorf("start %s: %w", name, err)
	}
	s.mu.Lock()
	s.processes[name] = process
	s.mu.Unlock()
	s.setStatus(name, stateStarting, "Process started; waiting for readiness")
	return nil
}

// failOperation records a failed stop or start so the status surface and the log
// show why the operator's action did not happen.
func (s *Supervisor) failOperation(name string, err error) error {
	s.setStatus(name, stateFailed, err.Error())
	s.appendLog(err.Error())
	return err
}

// describeHolder names the conflicting process when the platform can tell us,
// and states plainly that the address is occupied when it cannot.
func (s *Supervisor) describeHolder(address string) string {
	if holder := strings.TrimSpace(s.ports.Holder(address)); holder != "" {
		return holder
	}
	return "a process this session did not start"
}

// addressFree reports whether this process can bind the address right now.
func (s *Supervisor) addressFree(address string) bool {
	return strings.TrimSpace(address) == "" || s.ports.Free(address)
}

// waitAddressFree polls until the address can be bound again. A killed process
// group does not release its socket instantly, so the wait is bounded rather
// than immediate.
func (s *Supervisor) waitAddressFree(address string) bool {
	deadline := time.Now().Add(s.freeWindow)
	for {
		if s.addressFree(address) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(s.poll)
	}
}

func (s *Supervisor) waitReady(ctx context.Context, name, address string) error {
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	deadline := time.NewTimer(s.readyWait)
	defer deadline.Stop()
	for {
		if s.probe(ctx, address) {
			s.setStatus(name, stateReady, "Ready (started by this session)")
			return nil
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("readiness timeout for %s", address)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// StopAll terminates owned child processes and is safe to call repeatedly. It
// also verifies that every address this supervisor manages is actually free: a
// restart built on a stop that freed nothing would otherwise look successful.
func (s *Supervisor) StopAll(_ context.Context) error {
	s.mu.Lock()
	processes := s.processes
	s.processes = map[string]childProcess{}
	s.mu.Unlock()
	failures := make([]string, 0, len(processes))
	for name, process := range processes {
		if err := process.Stop(); err != nil {
			failures = append(failures, fmt.Sprintf("stop %s: %v", name, err))
			s.setStatus(name, stateFailed, fmt.Sprintf("stop failed: %v", err))
			continue
		}
		s.setStatus(name, stateStopped, "Stopped")
	}
	for _, component := range managedComponents {
		address := component.Address(s.config)
		if address == "" {
			continue
		}
		_, stopped := processes[component.Name]
		free := false
		if stopped {
			free = s.waitAddressFree(address)
		} else {
			free = s.addressFree(address)
		}
		if free {
			continue
		}
		// An adopted listener is not this session's to stop. The operator chose
		// to keep it running, so it is the outcome they asked for rather than a
		// stop that failed - and a restart must not be blocked by it.
		if entry, adopted := s.adoptedListenerFor(component.Name); adopted {
			s.setStatus(component.Name, stateAdopted, adoptedDetail(entry))
			continue
		}
		detail := fmt.Sprintf("%s is still held by %s; %s, or %s", address, s.describeHolder(address), resolveAdoptOffer, resolveTerminateOffer)
		failures = append(failures, component.Name+": "+detail)
		s.setStatus(component.Name, stateFailed, detail)
		s.appendLog(component.Name + ": " + detail)
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(failures, "; "))
}

func startCommand(ctx context.Context, executable string, args, extraEnv []string, output io.Writer) (childProcess, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("%s is unavailable: %w", executable, err)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	configureProcessGroup(cmd)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return managedProcess{process: cmd.Process}, nil
}

// runCommand executes a fixed subprocess with an argument array. It never uses a
// shell, so no argument can be interpreted as shell syntax.
func runCommand(ctx context.Context, executable string, args ...string) error {
	return exec.CommandContext(ctx, executable, args...).Run()
}

// netPortChecker binds the address to decide whether it is free. A process that
// answers a readiness probe without holding the port is not a conflict, and a
// process holding the port without answering is; only binding tells us which.
type netPortChecker struct{}

func (netPortChecker) Free(address string) bool {
	if strings.TrimSpace(address) == "" {
		return true
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// lsofListeners is the one place a listening process is resolved, so naming a
// holder and listing the holders to act on can never disagree about who holds an
// address.
func (netPortChecker) lsofListeners(address string) []listener {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, lsof, "-nP", "-sTCP:LISTEN", "-iTCP:"+port, "-Fpc").Output()
	if err != nil {
		return nil
	}
	return parseListenerHolders(string(output))
}

// Holder makes a best-effort attempt to name the process listening on an
// address. It returns an empty string whenever the platform cannot tell us; the
// caller still reports the address as occupied.
func (c netPortChecker) Holder(address string) string {
	return describeListeners(c.lsofListeners(address))
}

// Listeners returns the processes bound to an address, for the case where an
// operator has to choose what to do about one. An empty result means the
// platform could not tell us, which is not the same as nothing being there: the
// caller still refuses to start on an address it cannot bind.
func (c netPortChecker) Listeners(address string) []listener {
	return c.lsofListeners(address)
}

type managedProcess struct{ process *os.Process }

func (p managedProcess) Stop() error {
	if p.process == nil {
		return nil
	}
	stopErr := stopProcess(p.process)
	done := make(chan error, 1)
	go func() { _, err := p.process.Wait(); done <- err }()
	select {
	case waitErr := <-done:
		if stopErr != nil {
			return stopErr
		}
		return waitErr
	case <-time.After(5 * time.Second):
		_ = p.process.Kill()
		if stopErr != nil {
			return stopErr
		}
		return fmt.Errorf("process did not stop within 5s")
	}
}

type logWriter struct{ supervisor *Supervisor }

func (w logWriter) Write(bytes []byte) (int, error) {
	for _, line := range strings.Split(string(bytes), "\n") {
		w.supervisor.appendLog(line)
	}
	return len(bytes), nil
}
