package runtime

import (
	"context"
	"fmt"
	"io"
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
)

// ComponentStatus is safe to render in a terminal or desktop status surface.
type ComponentStatus struct {
	Name   string
	State  componentState
	Detail string
}

type childProcess interface {
	Signal(os.Signal) error
	Kill() error
}
type processStarter func(context.Context, string, []string, []string, io.Writer) (childProcess, error)

// Supervisor owns only the allow-listed local processes needed by Drift.
type Supervisor struct {
	config    Config
	dataDir   string
	start     processStarter
	client    *http.Client
	mu        sync.Mutex
	processes map[string]childProcess
	statuses  map[string]ComponentStatus
	logs      []string
}

func NewSupervisor(config Config, dataDir string) *Supervisor {
	return &Supervisor{config: config, dataDir: dataDir, start: startCommand, client: &http.Client{Timeout: 750 * time.Millisecond}, processes: map[string]childProcess{}, statuses: map[string]ComponentStatus{}}
}

func (s *Supervisor) setStatus(name string, state componentState, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[name] = ComponentStatus{Name: name, State: state, Detail: detail}
}

func (s *Supervisor) Status() []ComponentStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := []string{"Control Plane", "Device Service", "Desktop Application"}
	result := make([]ComponentStatus, 0, len(names))
	for _, name := range names {
		result = append(result, s.statuses[name])
	}
	return result
}
func (s *Supervisor) Logs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logs...)
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
	defer s.mu.Unlock()
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	line = strings.ReplaceAll(line, s.config.ServiceToken, "[REDACTED]")
	s.logs = append(s.logs, line)
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
}

// Setup validates local prerequisites and creates the owned storage/configuration.
func (s *Supervisor) Setup(ctx context.Context) error {
	adbPath, err := DefaultADBDiscovery(s.config.ADBPath)
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
	if err := exec.CommandContext(ctx, adbPath, "start-server").Run(); err != nil {
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
	if err := s.startComponent(ctx, "Control Plane", "go", []string{"run", "./cmd/control-plane"}, []string{
		"DRIFT_CONTROL_PLANE_ADDR=" + s.config.ControlPlaneAddress,
		"DRIFT_CONTROL_PLANE_DB=" + s.config.DatabasePath,
		"DRIFT_ARTIFACT_CAS_ROOT=" + s.config.ArtifactRoot,
		"DRIFT_RUNTIME_DEVICE_MODE=connected",
		"DRIFT_RUNTIME_ADB_PATH=" + s.config.ADBPath,
		"DRIFT_RUNTIME_SERVICE_TOKEN=" + s.config.ServiceToken,
	}); err != nil {
		return err
	}
	if err := s.waitReady(ctx, s.config.ControlPlaneAddress); err != nil {
		_ = s.StopAll(ctx)
		return fmt.Errorf("control plane readiness: %w", err)
	}
	if err := s.startComponent(ctx, "Device Service", "go", []string{"run", "./cmd/edge-agent"}, []string{"DRIFT_EDGE_AGENT_ADDR=" + s.config.EdgeAgentAddress}); err != nil {
		_ = s.StopAll(ctx)
		return err
	}
	if err := s.waitReady(ctx, s.config.EdgeAgentAddress); err != nil {
		_ = s.StopAll(ctx)
		return fmt.Errorf("device service readiness: %w", err)
	}
	if launchDesktop {
		if err := s.startComponent(ctx, "Desktop Application", "pnpm", []string{"--filter", "console", "exec", "tauri", "dev"}, []string{
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
		if err := s.startComponent(ctx, name, "go", []string{"run", "./cmd/control-plane"}, []string{"DRIFT_CONTROL_PLANE_ADDR=" + s.config.ControlPlaneAddress, "DRIFT_CONTROL_PLANE_DB=" + s.config.DatabasePath, "DRIFT_ARTIFACT_CAS_ROOT=" + s.config.ArtifactRoot, "DRIFT_RUNTIME_DEVICE_MODE=connected", "DRIFT_RUNTIME_ADB_PATH=" + s.config.ADBPath, "DRIFT_RUNTIME_SERVICE_TOKEN=" + s.config.ServiceToken}); err != nil {
			return err
		}
		return s.waitReady(ctx, s.config.ControlPlaneAddress)
	case "Device Service":
		if err := s.startComponent(ctx, name, "go", []string{"run", "./cmd/edge-agent"}, []string{"DRIFT_EDGE_AGENT_ADDR=" + s.config.EdgeAgentAddress}); err != nil {
			return err
		}
		return s.waitReady(ctx, s.config.EdgeAgentAddress)
	case "Desktop Application":
		return s.startComponent(ctx, name, "pnpm", []string{"--filter", "console", "exec", "tauri", "dev"}, []string{"VITE_DRIFT_CONTROL_PLANE_URL=http://" + s.config.ControlPlaneAddress, "VITE_DRIFT_RUNTIME_ADAPTER_URL=http://" + s.config.ControlPlaneAddress, "VITE_DRIFT_RUNTIME_SERVICE_TOKEN=" + s.config.ServiceToken, "VITE_DRIFT_RUNTIME_OPERATOR_ID=" + s.config.OperatorID})
	default:
		return fmt.Errorf("unknown component %q", name)
	}
}

// StopComponent stops one owned component and is idempotent.
func (s *Supervisor) StopComponent(name string) error {
	s.mu.Lock()
	process := s.processes[name]
	delete(s.processes, name)
	s.mu.Unlock()
	if process == nil {
		s.setStatus(name, stateStopped, "Stopped")
		return nil
	}
	if err := process.Signal(os.Interrupt); err != nil {
		_ = process.Kill()
		s.setStatus(name, stateStopped, "Stopped")
		return fmt.Errorf("stop %s: %w", name, err)
	}
	s.setStatus(name, stateStopped, "Stopped")
	return nil
}

func (s *Supervisor) startComponent(ctx context.Context, name, executable string, args, extraEnv []string) error {
	s.mu.Lock()
	if _, exists := s.processes[name]; exists {
		s.mu.Unlock()
		s.setStatus(name, stateReady, "Already running")
		return nil
	}
	s.mu.Unlock()
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

func (s *Supervisor) waitReady(ctx context.Context, address string) error {
	url := "http://" + address + "/readyz"
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		response, err := s.client.Do(req)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				if address == s.config.ControlPlaneAddress {
					s.setStatus("Control Plane", stateReady, "Ready")
				} else {
					s.setStatus("Device Service", stateReady, "Ready")
				}
				return nil
			}
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

// StopAll terminates owned child processes and is safe to call repeatedly.
func (s *Supervisor) StopAll(_ context.Context) error {
	s.mu.Lock()
	processes := s.processes
	s.processes = map[string]childProcess{}
	s.mu.Unlock()
	var first error
	for name, process := range processes {
		if err := process.Signal(os.Interrupt); err != nil {
			_ = process.Kill()
			if first == nil {
				first = fmt.Errorf("stop %s: %w", name, err)
			}
		}
		s.setStatus(name, stateStopped, "Stopped")
	}
	return first
}

func startCommand(ctx context.Context, executable string, args, extraEnv []string, output io.Writer) (childProcess, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("%s is unavailable: %w", executable, err)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

type logWriter struct{ supervisor *Supervisor }

func (w logWriter) Write(bytes []byte) (int, error) {
	for _, line := range strings.Split(string(bytes), "\n") {
		w.supervisor.appendLog(line)
	}
	return len(bytes), nil
}
