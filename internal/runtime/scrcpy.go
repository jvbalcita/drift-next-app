package runtime

import (
	"os"
	"runtime"
	"strings"
)

// mirrorServerPathEnv names the input the live mirror reads the host path of the
// scrcpy server from (internal/edge/mirror.EnvServerPath). It is spelled here,
// like every other variable this runtime hands a child process, because a child
// receives its configuration as text.
const mirrorServerPathEnv = "DRIFT_MIRROR_SCRCPY_SERVER"

// The two inputs the live mirror reads the workspace's preview setting from
// (internal/media.EnvPreviewQuality and EnvPreviewFrameRate). They are spelled
// here for the same reason every other variable this runtime hands a child is: a
// child receives its configuration as text, and the runtime must not depend on the
// plane's own package to name it.
const (
	mirrorPreviewQualityEnv   = "DRIFT_MIRROR_PREVIEW_QUALITY"
	mirrorPreviewFrameRateEnv = "DRIFT_MIRROR_PREVIEW_FRAME_RATE"
)

// scrcpyServerLocations returns the host paths a platform's own scrcpy
// installation keeps its device-side server at, in the order they are tried.
//
// These are the package managers' own locations rather than a search of the
// filesystem: a default that scanned for a file named like a server could
// resolve a deployment onto a file nobody installed for it, and where the server
// came from is exactly what makes it trustworthy to push onto a device. An
// installation that is not one of these is configured explicitly, and the
// startup line names the input that is missing rather than guessing at one.
func scrcpyServerLocations(goos string) []string {
	switch goos {
	case "darwin":
		return []string{
			// Apple-silicon and Intel Homebrew prefixes.
			"/opt/homebrew/share/scrcpy/scrcpy-server",
			"/usr/local/share/scrcpy/scrcpy-server",
		}
	case "linux":
		return []string{
			// Distribution package, manual install, Linuxbrew.
			"/usr/share/scrcpy/scrcpy-server",
			"/usr/local/share/scrcpy/scrcpy-server",
			"/home/linuxbrew/.linuxbrew/share/scrcpy/scrcpy-server",
		}
	default:
		// Windows has no fixed installation location: scrcpy ships there as an
		// archive an operator unpacks where they choose, so there is no platform
		// location to resolve and this input is configured.
		return nil
	}
}

// DiscoverScrcpyServer resolves the host path of the scrcpy server binary the
// live mirror pushes to each device.
//
// It never fails, and it never invents a path. A host with no usable server is a
// deployment that has to be told which input is missing, and the dialer reading
// this value is what names it: a path that is not there is a mirror that arms
// and shows nothing, where no path at all is a mirror whose own startup line
// says why it did not arm.
//
// The order is deliberate. A path this deployment configured is used as
// configured, whether or not it resolves here, because a deployment pointed at
// the wrong file must fail where it is configured rather than be corrected
// silently. Then an explicit value in the launching environment, which is how an
// operator armed the mirror by hand before this runtime supplied it: that export
// keeps working and is never overridden by a value discovered here. Only then
// does the platform's own installation apply - and only when the file is
// actually there, never as a path that merely looks like one.
func DiscoverScrcpyServer(explicit string, lookupEnv func(string) (string, bool), stat func(string) (os.FileInfo, error), goos string) string {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if stat == nil {
		stat = os.Stat
	}
	if path := strings.TrimSpace(explicit); path != "" {
		return path
	}
	if value, ok := lookupEnv(mirrorServerPathEnv); ok {
		if path := strings.TrimSpace(value); path != "" {
			return path
		}
	}
	for _, candidate := range scrcpyServerLocations(goos) {
		info, err := stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		return candidate
	}
	return ""
}

// DefaultScrcpyServerDiscovery is the production resolver.
func DefaultScrcpyServerDiscovery(explicit string) string {
	return DiscoverScrcpyServer(explicit, os.LookupEnv, os.Stat, runtime.GOOS)
}
