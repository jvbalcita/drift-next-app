package runtime_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"drift.local/drift-next/internal/runtime"
)

// seamInfo is the least an os.FileInfo has to be for this resolver: whether the
// candidate is there, and whether it is a file rather than a directory.
type seamInfo struct{ dir bool }

func (i seamInfo) Name() string       { return "scrcpy-server" }
func (i seamInfo) Size() int64        { return 0 }
func (i seamInfo) ModTime() time.Time { return time.Time{} }
func (i seamInfo) IsDir() bool        { return i.dir }
func (i seamInfo) Sys() any           { return nil }

func (i seamInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}

// present reports the candidates that exist on this fake host, and nothing the
// host does not have: a resolver that named a path `stat` refused would be naming
// a file that is not there.
func present(paths ...string) func(string) (os.FileInfo, error) {
	existing := map[string]os.FileInfo{}
	for _, path := range paths {
		existing[path] = seamInfo{}
	}
	return func(path string) (os.FileInfo, error) {
		if info, ok := existing[path]; ok {
			return info, nil
		}
		return nil, errors.New("no such file or directory")
	}
}

// directoryAt reports a directory sitting where a server is expected - the
// misconfiguration where the operator's installation directory is named - and
// nothing else on this fake host.
func directoryAt(path string) func(string) (os.FileInfo, error) {
	return func(candidate string) (os.FileInfo, error) {
		if candidate == path {
			return seamInfo{dir: true}, nil
		}
		return nil, errors.New("no such file or directory")
	}
}

// configuredEnv is a deployment's environment as a map, so each case states
// exactly what the launching shell exported.
func configuredEnv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

// A deployment's own configuration is what arms the mirror: a path set in the
// runtime configuration is handed over as configured, before anything here has
// checked it, because a deployment pointed at the wrong file must fail where it
// is configured rather than be quietly corrected into a different mirror. The
// dialer asks the same question one step later, with a message naming this input.
func TestDiscoverScrcpyServerUsesTheConfiguredPathAsConfigured(t *testing.T) {
	discovered := runtime.DiscoverScrcpyServer(
		"/opt/deploy/scrcpy-server",
		configuredEnv(map[string]string{"DRIFT_MIRROR_SCRCPY_SERVER": "/from/shell/scrcpy-server"}),
		present("/opt/homebrew/share/scrcpy/scrcpy-server"),
		"darwin",
	)
	if discovered != "/opt/deploy/scrcpy-server" {
		t.Fatalf("a configured path resolved to %q, want the path the deployment configured", discovered)
	}
}

// An operator who armed the mirror by exporting the variable keeps working: the
// export is used, and it is never overridden by a location discovered here.
func TestDiscoverScrcpyServerHonoursAnExportedPathOverADiscoveredOne(t *testing.T) {
	discovered := runtime.DiscoverScrcpyServer(
		"",
		configuredEnv(map[string]string{"DRIFT_MIRROR_SCRCPY_SERVER": " /from/shell/scrcpy-server "}),
		present("/opt/homebrew/share/scrcpy/scrcpy-server"),
		"darwin",
	)
	if discovered != "/from/shell/scrcpy-server" {
		t.Fatalf("an exported server path resolved to %q, want the exported path", discovered)
	}
}

// With nothing configured, the platform's own installation is what arms the
// mirror - and a deployment that configured only whitespace has configured
// nothing, so that installation is still what arms it.
func TestDiscoverScrcpyServerFallsBackToThePlatformsOwnInstallation(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		goos     string
		server   string
		explicit string
		env      map[string]string
	}{
		{name: "Apple-silicon Homebrew", goos: "darwin", server: "/opt/homebrew/share/scrcpy/scrcpy-server"},
		{name: "Intel Homebrew", goos: "darwin", server: "/usr/local/share/scrcpy/scrcpy-server"},
		{name: "distribution package", goos: "linux", server: "/usr/share/scrcpy/scrcpy-server"},
		{name: "manual installation", goos: "linux", server: "/usr/local/share/scrcpy/scrcpy-server"},
		{name: "Linuxbrew", goos: "linux", server: "/home/linuxbrew/.linuxbrew/share/scrcpy/scrcpy-server"},
		{
			name:     "a blank configured path is not a configured one",
			goos:     "darwin",
			server:   "/opt/homebrew/share/scrcpy/scrcpy-server",
			explicit: "   ",
		},
		{
			name:   "a blank export is not a configured one",
			goos:   "darwin",
			server: "/opt/homebrew/share/scrcpy/scrcpy-server",
			env:    map[string]string{"DRIFT_MIRROR_SCRCPY_SERVER": "  "},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			discovered := runtime.DiscoverScrcpyServer(
				testCase.explicit,
				configuredEnv(testCase.env),
				present(testCase.server),
				testCase.goos,
			)
			if discovered != testCase.server {
				t.Fatalf("resolved %q, want the platform's own installation at %q", discovered, testCase.server)
			}
		})
	}
}

// No server anywhere is reported as no server. This resolver never invents a
// path: a path that is not there is a mirror that arms and shows nothing, where
// no path at all is a mirror whose own startup line says why.
func TestDiscoverScrcpyServerInventsNothing(t *testing.T) {
	for _, testCase := range []struct {
		name string
		stat func(string) (os.FileInfo, error)
		goos string
	}{
		{
			name: "nothing configured and nothing installed",
			stat: present(),
			goos: "darwin",
		},
		{
			name: "a directory where the server is expected",
			stat: directoryAt("/opt/homebrew/share/scrcpy/scrcpy-server"),
			goos: "darwin",
		},
		{
			name: "a distribution directory that holds no server",
			stat: directoryAt("/usr/share/scrcpy/scrcpy-server"),
			goos: "linux",
		},
		{
			name: "a platform with no fixed installation location",
			// Windows ships scrcpy as an archive an operator unpacks where they
			// choose, so there is no platform location and this input is
			// configured; a file being there by coincidence is not a location.
			stat: directoryAt("/usr/share/scrcpy/scrcpy-server"),
			goos: "windows",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			discovered := runtime.DiscoverScrcpyServer("", configuredEnv(nil), testCase.stat, testCase.goos)
			if discovered != "" {
				t.Fatalf("resolver invented %q where this host has no server", discovered)
			}
		})
	}
}

// The production resolver only ever names a file that is on this host. That is
// the one property a machine-dependent default can be held to wherever it runs:
// a host with scrcpy installed resolves it, a host without resolves nothing, and
// neither names a path that is not there.
func TestDefaultScrcpyServerDiscoveryOnlyNamesAFileThatIsThere(t *testing.T) {
	discovered := runtime.DefaultScrcpyServerDiscovery("")
	if discovered == "" {
		return
	}
	info, err := os.Stat(discovered)
	if err != nil {
		t.Fatalf("the default resolver named %q, which is not on this host: %v", discovered, err)
	}
	if info.IsDir() {
		t.Fatalf("the default resolver named %q, which is a directory", discovered)
	}
}

// The production wrapper is the resolver with this host's environment and its
// own platform, and it still uses a configured path as configured.
func TestDefaultScrcpyServerDiscoveryUsesAConfiguredPath(t *testing.T) {
	const configured = "/opt/deploy/scrcpy-server"
	if discovered := runtime.DefaultScrcpyServerDiscovery("  " + configured + "  "); discovered != configured {
		t.Fatalf("a configured path resolved to %q, want %q", discovered, configured)
	}
}
