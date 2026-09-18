// Package runtime owns the local, restart-safe application runtime bootstrap.
package runtime

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const configFileName = "runtime.json"

// Config is the non-secret runtime configuration persisted for the local app.
// The token is intentionally never included in String or status output.
type Config struct {
	ControlPlaneAddress string `json:"control_plane_address"`
	EdgeAgentAddress    string `json:"edge_agent_address"`
	DatabasePath        string `json:"database_path"`
	ArtifactRoot        string `json:"artifact_root"`
	ADBPath             string `json:"adb_path"`
	OperatorID          string `json:"operator_id"`
	ServiceToken        string `json:"service_token"`
	// ScrcpyServerPath is the absolute host path of the scrcpy server the live
	// mirror pushes to each device (the mirror's EnvServerPath,
	// DRIFT_MIRROR_SCRCPY_SERVER). It is a deployment input like ADBPath, and an
	// empty value is not an error: it means "resolve the platform's own scrcpy
	// installation", which the runtime does at setup and passes to the control
	// plane. A path set here is handed over as configured, so a deployment
	// pointed at the wrong file fails where it is configured rather than being
	// quietly replaced with a discovered one.
	//
	// It is not persisted when resolution leaves it empty, so a discovered path
	// is never frozen into this file: an operator who upgrades scrcpy gets the
	// new server without editing their configuration.
	ScrcpyServerPath string `json:"scrcpy_server_path,omitempty"`
}

// DataDir returns the application-owned local data directory.
func DataDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		// Keep this identifier aligned with Tauri's app_config_dir() so the
		// supervisor and the installed desktop shell share one runtime.json.
		return filepath.Join(dir, "com.drift.commandcenter")
	}
	return filepath.Join("var", "com.drift.commandcenter")
}

func configPath(dataDir string) string { return filepath.Join(dataDir, configFileName) }

// LoadOrCreate creates durable local configuration once and reloads it on later
// starts. It does not print or return secrets except to the owning supervisor.
func LoadOrCreate(dataDir string) (Config, error) {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = DataDir()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create application data directory: %w", err)
	}
	path := configPath(dataDir)
	if bytes, err := os.ReadFile(path); err == nil {
		var cfg Config
		if err := json.Unmarshal(bytes, &cfg); err != nil {
			return Config{}, fmt.Errorf("read runtime configuration: %w", err)
		}
		if err := cfg.validate(); err != nil {
			return Config{}, fmt.Errorf("validate runtime configuration: %w", err)
		}
		return cfg, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read runtime configuration: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		return Config{}, fmt.Errorf("generate service credential: %w", err)
	}
	cfg := Config{
		ControlPlaneAddress: "127.0.0.1:8080",
		EdgeAgentAddress:    "127.0.0.1:8081",
		DatabasePath:        filepath.Join(dataDir, "control-plane.db"),
		ArtifactRoot:        filepath.Join(dataDir, "artifacts", "cas"),
		OperatorID:          "operator-local",
		ServiceToken:        token,
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	bytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Config{}, fmt.Errorf("encode runtime configuration: %w", err)
	}
	tmp, err := os.CreateTemp(dataDir, ".runtime-*.tmp")
	if err != nil {
		return Config{}, fmt.Errorf("create runtime configuration: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return Config{}, fmt.Errorf("protect runtime configuration: %w", err)
	}
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		return Config{}, fmt.Errorf("write runtime configuration: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Config{}, fmt.Errorf("close runtime configuration: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return Config{}, fmt.Errorf("install runtime configuration: %w", err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	for name, value := range map[string]string{
		"control plane address": c.ControlPlaneAddress,
		"edge agent address":    c.EdgeAgentAddress,
		"database path":         c.DatabasePath,
		"artifact root":         c.ArtifactRoot,
		"operator ID":           c.OperatorID,
		"service token":         c.ServiceToken,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
