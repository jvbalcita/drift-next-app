package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DiscoverADB resolves adb from PATH and standard Android SDK locations.
// An explicit path is accepted only as a fallback and must be executable.
func DiscoverADB(explicit string, lookupPath func(string) (string, error), stat func(string) (os.FileInfo, error), goos string, home string) (string, error) {
	if lookupPath == nil {
		lookupPath = exec.LookPath
	}
	if stat == nil {
		stat = os.Stat
	}
	candidates := make([]string, 0, 8)
	if path, err := lookupPath("adb"); err == nil {
		candidates = append(candidates, path)
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if sdk := os.Getenv("ANDROID_HOME"); sdk != "" {
		candidates = append(candidates, filepath.Join(sdk, "platform-tools", adbName(goos)))
	}
	if sdk := os.Getenv("ANDROID_SDK_ROOT"); sdk != "" {
		candidates = append(candidates, filepath.Join(sdk, "platform-tools", adbName(goos)))
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, "Library", "Android", "sdk", "platform-tools", adbName(goos)),
			filepath.Join(home, "Android", "Sdk", "platform-tools", adbName(goos)),
		)
	}
	// An explicit path is a fallback for non-standard installations.
	if strings.TrimSpace(explicit) != "" {
		candidates = append(candidates, explicit)
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		info, err := stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("adb was not found on PATH or in standard Android SDK locations")
}

func adbName(goos string) string {
	if goos == "windows" {
		return "adb.exe"
	}
	return "adb"
}

// DefaultADBDiscovery is the production resolver.
func DefaultADBDiscovery(explicit string) (string, error) {
	return DiscoverADB(explicit, exec.LookPath, os.Stat, runtime.GOOS, "")
}
