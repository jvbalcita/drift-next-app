package connection

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/adb"
)

// This is the card's done-bar item 1: the tool's OWN code path takes an endpoint from
// not-connected to `device`, with before and after state produced by the tool and not
// by a manual shell command.
//
// It is opt-in, and it is NOT a fake: a fake cannot produce this evidence. It drives
// the real adapter against the lab fleet, so it drops and re-establishes the adb
// server's transports for real. Set DRIFT_ADB_LAB_ENDPOINTS to the endpoints to
// recover, comma separated, to run it:
//
//	DRIFT_ADB_LAB_ENDPOINTS=192.168.1.109:5555,192.168.1.110:5555 \
//	  go test ./internal/edge/connection/ -run TestRestartRecoversTheLabFleet -v
//
// The device state is read through the adapter's own typed enumeration rather than by
// shelling out to `adb devices -l` a second time, because the point of the requirement
// is that the tool produced the observation, not that a particular command text appears.
func TestRestartRecoversTheLabFleetThroughTheToolsOwnPath(t *testing.T) {
	configured := strings.TrimSpace(os.Getenv("DRIFT_ADB_LAB_ENDPOINTS"))
	if configured == "" {
		t.Skip("set DRIFT_ADB_LAB_ENDPOINTS=ip:port[,ip:port] to run the restart against the lab fleet; it drops and re-establishes real transports")
	}

	executable, err := exec.LookPath("adb")
	if err != nil {
		t.Fatalf("adb is not on PATH: %v", err)
	}
	runner, err := adb.NewProcessRunner()
	if err != nil {
		t.Fatalf("NewProcessRunner: %v", err)
	}
	adapter, err := adb.NewAdapter(executable, runner)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	endpoints := make([]string, 0, 4)
	ports := make([]uint16, 0, 4)
	seenPorts := map[uint16]struct{}{}
	for _, raw := range strings.Split(configured, ",") {
		endpoint := strings.TrimSpace(raw)
		if endpoint == "" {
			continue
		}
		port, err := adb.EndpointPort(endpoint)
		if err != nil {
			t.Fatalf("DRIFT_ADB_LAB_ENDPOINTS contains %q, which is not an IPv4:port endpoint: %v", endpoint, err)
		}
		endpoints = append(endpoints, endpoint)
		if _, seen := seenPorts[port]; !seen {
			seenPorts[port] = struct{}{}
			ports = append(ports, port)
		}
	}
	if len(endpoints) == 0 {
		t.Skip("DRIFT_ADB_LAB_ENDPOINTS held no usable endpoint")
	}

	restarter, err := NewRestarter(RestarterConfig{
		Runner:     adapter,
		Enumerator: adapter,
		Policy:     NewPortPolicy(ports),
	})
	if err != nil {
		t.Fatalf("NewRestarter: %v", err)
	}

	// BEFORE, produced by the tool.
	reportEnumeration(t, ctx, adapter, "BEFORE — the tool's own view of the fleet")

	started := time.Now()
	outcome, err := restarter.Restart(ctx, endpoints)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	fmt.Printf("\nRESTART — what it actually did (%s)\n", time.Since(started).Round(time.Millisecond))
	fmt.Printf("  transports before the restart : %d\n", outcome.TransportsBefore)
	fmt.Printf("  transports after start-server : %d\n", outcome.TransportsAfterStart)
	fmt.Printf("  kill-server exit code         : %d (failed=%t)\n", outcome.KillExitCode, outcome.KillFailed)
	fmt.Printf("  start-server exit code        : %d (failed=%t)\n", outcome.StartExitCode, outcome.StartFailed)
	fmt.Printf("  re-established / failed       : %d / %d\n", outcome.Reestablished, outcome.Failed)
	for _, entry := range outcome.Endpoints {
		verdict := "re-established"
		if !entry.Reestablished {
			verdict = fmt.Sprintf("DID NOT COME BACK: %v", entry.Err)
		}
		fmt.Printf("  %-22s port %-5d exit %-3d %s\n", entry.Endpoint, entry.Port, entry.ExitCode, verdict)
	}

	// AFTER, produced by the tool.
	states := reportEnumeration(t, ctx, adapter, "AFTER — the tool's own view of the fleet")

	// Every endpoint must be attached. This is the assertion the done bar asks for: the
	// tool's code path took the fleet from no transports to `device`.
	for _, endpoint := range endpoints {
		state, observed := states[endpoint]
		if !observed {
			t.Fatalf("endpoint %s is absent from the fleet after the restart; it was not re-established", endpoint)
		}
		if !strings.EqualFold(state, "device") {
			t.Fatalf("endpoint %s came back in state %q, want device", endpoint, state)
		}
	}
	if outcome.Failed != 0 {
		t.Fatalf("%d endpoint(s) did not come back; the per-endpoint report above names them", outcome.Failed)
	}
}

// reportEnumeration prints what the tool sees and returns each attached endpoint's
// state, keyed by the endpoint the fleet reports.
func reportEnumeration(t *testing.T, ctx context.Context, adapter *adb.Adapter, title string) map[string]string {
	t.Helper()
	devices, err := adapter.Enumerate(ctx)
	if err != nil {
		t.Fatalf("%s: enumerate: %v", title, err)
	}
	fmt.Printf("\n%s — %d transport(s)\n", title, len(devices))
	states := make(map[string]string, len(devices))
	for _, device := range devices {
		state := string(device.State)
		key := device.Serial
		if device.TransportID != "" && strings.Contains(device.TransportID, ":") {
			key = strings.TrimPrefix(device.TransportID, "tcp:")
		}
		states[key] = state
		fmt.Printf("  %-22s state=%-12s model=%-10s device=%-10s transport=%s connection=%s\n",
			key, state, device.Model, device.Device, device.TransportID, device.ConnectionType)
	}
	return states
}
