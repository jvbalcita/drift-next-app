package runtime

import (
	"context"
	"io"
	"strings"
	"testing"
)

// startedChild records one child this session started, with the exact
// configuration it was handed, so a test can read what the deployment gave a
// component rather than what it intended to.
type startedChild struct {
	executable string
	args       []string
	env        []string
}

// newMirrorTestSupervisor builds a supervisor whose child starts are recorded
// and whose scrcpy server resolution is fixed by the test. Everything else comes
// from the shared fake seams: no process, port, or HTTP request is real.
func newMirrorTestSupervisor(t *testing.T, serverPath string) (*Supervisor, *[]startedChild) {
	t.Helper()
	serving := map[string]bool{"127.0.0.1:18080": true, "127.0.0.1:18081": true}
	supervisor, _ := newTestSupervisor(t, newFakePortChecker(), serving)
	supervisor.discoverScrcpyServer = func(string) string { return serverPath }
	children := &[]startedChild{}
	supervisor.start = func(_ context.Context, executable string, args, env []string, _ io.Writer) (childProcess, error) {
		*children = append(*children, startedChild{
			executable: executable,
			args:       append([]string(nil), args...),
			env:        append([]string(nil), env...),
		})
		return &fakeChildProcess{}, nil
	}
	return supervisor, children
}

// controlPlaneChild returns the recorded child that is the control plane's own
// process, identified by the command it was started with rather than by the
// order the components happen to start in.
func controlPlaneChild(t *testing.T, children []startedChild) startedChild {
	t.Helper()
	for _, child := range children {
		if child.executable == "go" && strings.Join(child.args, " ") == "run ./cmd/control-plane" {
			return child
		}
	}
	t.Fatalf("no control plane child was started; children = %+v", children)
	return startedChild{}
}

// childEnvValue reads one variable out of a child's configuration.
func childEnvValue(env []string, key string) (string, bool) {
	for _, entry := range env {
		if name, value, found := strings.Cut(entry, "="); found && name == key {
			return value, true
		}
	}
	return "", false
}

// The TUI is the deployment: launching it is how an operator asks for a mirror,
// so both of the control plane's start paths must hand the mirror its server
// path. Start All is the launch an operator uses, and starting the component
// alone is the troubleshooting path the console offers - a deployment input that
// arrives on one and not the other is the same defect on whichever path was
// forgotten.
func TestEveryControlPlaneStartPathHandsTheMirrorItsScrcpyServer(t *testing.T) {
	const serverPath = "/opt/homebrew/share/scrcpy/scrcpy-server"
	for _, testCase := range []struct {
		name  string
		start func(*Supervisor) error
	}{
		{
			name: "Start All",
			start: func(supervisor *Supervisor) error {
				return supervisor.StartAll(context.Background(), false)
			},
		},
		{
			name: "Start Component",
			start: func(supervisor *Supervisor) error {
				return supervisor.StartComponent(context.Background(), "Control Plane")
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			supervisor, children := newMirrorTestSupervisor(t, serverPath)
			if err := testCase.start(supervisor); err != nil {
				t.Fatalf("%s failed: %v", testCase.name, err)
			}
			child := controlPlaneChild(t, *children)
			value, ok := childEnvValue(child.env, mirrorServerPathEnv)
			if !ok {
				t.Fatalf("%s did not hand the control plane %s: %v", testCase.name, mirrorServerPathEnv, child.env)
			}
			if value != serverPath {
				t.Fatalf("%s handed %s=%q, want the resolved %q", testCase.name, mirrorServerPathEnv, value, serverPath)
			}
		})
	}
}

// Every other deployment input still travels exactly as before: the mirror's
// input was added to the list the control plane is built from, not swapped for
// one of them.
func TestTheControlPlaneChildKeepsEveryOtherDeploymentInput(t *testing.T) {
	const serverPath = "/usr/share/scrcpy/scrcpy-server"
	supervisor, children := newMirrorTestSupervisor(t, serverPath)
	if err := supervisor.StartAll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	child := controlPlaneChild(t, *children)
	for _, expected := range []struct{ key, value string }{
		{"DRIFT_CONTROL_PLANE_ADDR", supervisor.config.ControlPlaneAddress},
		{"DRIFT_CONTROL_PLANE_DB", supervisor.config.DatabasePath},
		{"DRIFT_ARTIFACT_CAS_ROOT", supervisor.config.ArtifactRoot},
		{"DRIFT_RUNTIME_DEVICE_MODE", "connected"},
		{"DRIFT_RUNTIME_ADB_PATH", supervisor.config.ADBPath},
		{"DRIFT_RUNTIME_SERVICE_TOKEN", supervisor.config.ServiceToken},
		{mirrorServerPathEnv, serverPath},
	} {
		value, ok := childEnvValue(child.env, expected.key)
		if !ok || value != expected.value {
			t.Errorf("the control plane child was handed %s=%q (present=%v), want %q", expected.key, value, ok, expected.value)
		}
	}
	// The screen hold is the operator's own explicit choice. This runtime does
	// not set it, so the dialer's default is what a deployment gets unless the
	// operator says otherwise.
	if value, ok := childEnvValue(child.env, "DRIFT_MIRROR_KEEP_AWAKE"); ok {
		t.Errorf("the runtime chose the screen hold for the operator: %s=%q", "DRIFT_MIRROR_KEEP_AWAKE", value)
	}
}

// A server that could not be resolved is passed as nothing at all, never as an
// empty value: the dialer refuses an unset input with a message naming it, where
// an empty value it accepted would be a mirror that armed and showed nothing. The
// deployment still starts - the rest of the product does not need a mirror - and
// the startup line carries the same diagnosis into the frame.
func TestAControlPlaneWithoutAResolvedScrcpyServerIsGivenNoValueAtAll(t *testing.T) {
	supervisor, children := newMirrorTestSupervisor(t, "")
	if err := supervisor.StartAll(context.Background(), false); err != nil {
		t.Fatalf("an unresolvable scrcpy server must not stop the deployment: %v", err)
	}
	child := controlPlaneChild(t, *children)
	if value, ok := childEnvValue(child.env, mirrorServerPathEnv); ok {
		t.Fatalf("the control plane child was handed %s=%q; an unresolved server is passed as nothing", mirrorServerPathEnv, value)
	}
	var startupLine string
	for _, line := range supervisor.Logs() {
		if strings.Contains(line, "no scrcpy server resolved") {
			startupLine = line
		}
	}
	if startupLine == "" {
		t.Fatalf("no startup line reports the missing scrcpy server: %v", supervisor.Logs())
	}
	if !strings.Contains(startupLine, mirrorServerPathEnv) {
		t.Errorf("the startup line %q does not name %s, so it does not say what to set", startupLine, mirrorServerPathEnv)
	}
}

// The startup line names the server this session resolved, so a frame whose
// mirror shows nothing already carries the diagnosis.
func TestSetupReportsTheResolvedScrcpyServerInTheStartupLine(t *testing.T) {
	const serverPath = "/opt/homebrew/share/scrcpy/scrcpy-server"
	supervisor, _ := newMirrorTestSupervisor(t, serverPath)
	if err := supervisor.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, line := range supervisor.Logs() {
		if strings.Contains(line, serverPath) {
			return
		}
	}
	t.Fatalf("no startup line names the resolved server %q: %v", serverPath, supervisor.Logs())
}

// Resolution is asked about the deployment's own value, and what it resolves is
// what the control plane is handed: a path configured in runtime.json reaches
// the mirror rather than being replaced by a discovery.
func TestSetupResolvesTheServersPathFromTheDeploymentsOwnValue(t *testing.T) {
	const configured = "/opt/deploy/scrcpy-server"
	supervisor, children := newMirrorTestSupervisor(t, configured)
	supervisor.config.ScrcpyServerPath = configured
	asked := ""
	supervisor.discoverScrcpyServer = func(explicit string) string {
		asked = explicit
		return explicit
	}
	if err := supervisor.StartAll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if asked != configured {
		t.Fatalf("resolution was asked about %q, want the configured %q", asked, configured)
	}
	child := controlPlaneChild(t, *children)
	if value, ok := childEnvValue(child.env, mirrorServerPathEnv); !ok || value != configured {
		t.Fatalf("the control plane child was handed %s=%q (present=%v), want the configured %q", mirrorServerPathEnv, value, ok, configured)
	}
}
