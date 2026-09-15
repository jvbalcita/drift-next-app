package product

import (
	"os"
	"path/filepath"
)

// DefaultControlPlaneDBPath is the loopback-local SQLite file used when
// DRIFT_CONTROL_PLANE_DB is unset. It prefers the operator's config directory
// and falls back to a workspace-relative path; it is never an ephemeral temp
// file and never encodes secrets.
func DefaultControlPlaneDBPath() string {
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "drift-next", "control-plane.db")
	}
	return filepath.Join("var", "drift-next", "control-plane.db")
}
