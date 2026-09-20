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

	"drift.local/drift-next/internal/media"
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
	// PreviewQuality and PreviewFrameRate are the workspace's preview setting as
	// this deployment states it (the mirror's EnvPreviewQuality and
	// EnvPreviewFrameRate, DRIFT_MIRROR_PREVIEW_QUALITY and
	// DRIFT_MIRROR_PREVIEW_FRAME_RATE): the bound the console's grid is carried
	// at, as a level and a capture rate.
	//
	// They are carried as TEXT and not as a level and a number, because the plane
	// is the one that decides what they mean: a value configured here is handed
	// over exactly as it was stated, and the plane's own reader is what refuses a
	// level or a rate it cannot bound - naming the input and the value, which is
	// the diagnosis an operator needs. A runtime that validated them itself would
	// be a second opinion about the same bound, and the two could disagree.
	//
	// An empty value is not an error and is NOT handed over: it means "the plane's
	// own documented default", which is itself a level with a cap. Passing an
	// empty value instead would be handing the plane a setting nobody stated.
	//
	// PreviewQuality is also the PROFILE the plane's live-stream budget is spent
	// at: a stream at that level is what the transport carries, so what one of
	// them costs is read from the same level rather than stated a second time
	// here. A deployment that chose High for its grid is a deployment whose grid
	// carries fewer tiles, and that follows from this one input.
	PreviewQuality   string `json:"preview_quality,omitempty"`
	PreviewFrameRate string `json:"preview_frame_rate,omitempty"`

	// MirrorSessionCapacity, MirrorOperatorReserve and MirrorTransportBudgetKbps
	// are the live mirror's own deployment inputs, and they live here because this
	// file is where a deployment states what this host is: how many devices it will
	// mirror at once, how many of those places are kept for the operator's own
	// frame, and how much of the transport its live streams may spend.
	//
	// They are carried into the control plane's environment on EVERY start path
	// (see controlPlaneEnv), so a deployment can never pass the capacity on the
	// path an operator happened to use and forget it on the other. Zero means
	// "the plane's own documented default", which the plane states in its startup
	// line; a value that IS stated is validated here as well as there, so a mistyped
	// bound is refused where the operator configured it rather than at a viewer's
	// request.
	//
	// A new deployment is written with the values measured on this fleet rather
	// than with none, because a bound nobody wrote down is a bound nobody can read
	// back: MirrorSessionCapacity 4 with MirrorOperatorReserve 1 is the measured
	// concurrency and the grid it leaves, and MirrorTransportBudgetKbps is the
	// largest aggregate that was measured carried (see
	// docs/operations/live-stream-concurrency.md, and the startup line this
	// deployment's own runtime reports for the values actually in force).
	MirrorSessionCapacity     int `json:"mirror_session_capacity,omitempty"`
	MirrorOperatorReserve     int `json:"mirror_operator_reserve,omitempty"`
	MirrorTransportBudgetKbps int `json:"mirror_transport_budget_kbps,omitempty"`
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
		// The live mirror's bound is written down for a new deployment rather than
		// left to the plane's defaults, because a bound nobody wrote down is a
		// bound nobody can read back. These are the values measured on this fleet:
		// four concurrent live sessions held 30 fps each while a fifth could not
		// open at all, one place of the four kept for the operator's own frame, and
		// the largest aggregate that was measured carried
		// (docs/operations/live-stream-concurrency.md). What one of the grid's
		// streams costs the transport is not stated here: it is read from the
		// preview level above, which is the level those streams are carried at.
		MirrorSessionCapacity:     media.DefaultMirrorSessionCapacity,
		MirrorOperatorReserve:     media.DefaultOperatorReserve,
		MirrorTransportBudgetKbps: media.DefaultTransportBudgetKbps,
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
	return c.validateMirrorBound()
}

// validateMirrorBound refuses a live-mirror bound this deployment could not mean.
//
// It is checked here as well as in the control plane because this is where the
// operator configured it: a bound that is negative, or a reserve that is the whole
// capacity, is a startup line naming the input rather than a fleet of tiles that
// behaves inexplicably.
//
// The pair is resolved against the same documented defaults the plane resolves an
// unstated value to, so a deployment that states only ONE of the two is checked as
// the pair the plane will actually carry: a capacity of one with no stated reserve
// is the plane's default reserve of one, which is a reserve that is the whole
// capacity, and it is refused here rather than a host that starts and then refuses
// every tile.
//
// What a stream at the deployment's preview level COSTS is not checked here: the
// level is handed to the plane as stated text and the plane is the one that prices
// it (see the PreviewQuality field), so a budget below that cost is refused by the
// plane with the two numbers in its sentence.
func (c Config) validateMirrorBound() error {
	for name, value := range map[string]int{
		"mirror_session_capacity":      c.MirrorSessionCapacity,
		"mirror_operator_reserve":      c.MirrorOperatorReserve,
		"mirror_transport_budget_kbps": c.MirrorTransportBudgetKbps,
	} {
		if value < 0 {
			return fmt.Errorf("%s must be a positive whole number, and this deployment configured %d", name, value)
		}
	}
	capacity := c.MirrorSessionCapacity
	if capacity <= 0 {
		capacity = media.DefaultMirrorSessionCapacity
	}
	reserve := c.MirrorOperatorReserve
	if reserve <= 0 {
		reserve = media.DefaultOperatorReserve
	}
	if reserve >= capacity {
		return fmt.Errorf(
			"mirror_operator_reserve (%d) must be smaller than mirror_session_capacity (%d): a reserve that is the whole capacity leaves the console's grid no place at all",
			reserve, capacity)
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
