package adb

import (
	"context"
	"errors"
	"testing"
)

// endpoint is the shape every connection admission accepts: the lab fleet's own
// addressing, canonical IPv4 and port.
const endpoint = "192.168.1.109:5555"

func connectionArrays(t *testing.T) map[string][]string {
	t.Helper()
	connect, err := ConnectArgv(endpoint)
	if err != nil {
		t.Fatalf("ConnectArgv(%q) = %v", endpoint, err)
	}
	return map[string][]string{
		"connect":      connect,
		"kill-server":  KillServerArgv(),
		"start-server": StartServerArgv(),
	}
}

// Each connection command is admitted under its own operation name, so a caller
// can tell which one ran and so no two of them can be confused in a record.
func TestTheConnectionAdmissionsAreRecognisedWithTheirOwnOperationNames(t *testing.T) {
	names := map[string]bool{}
	for wantName, args := range connectionArrays(t) {
		name, ok := matchesHostAllowlist(args)
		if !ok {
			t.Fatalf("%v is not admitted by the host recogniser", args)
		}
		if name != wantName {
			t.Fatalf("%v is admitted as %q, want %q", args, name, wantName)
		}
		if names[name] {
			t.Fatalf("operation name %q is used by more than one admission", name)
		}
		names[name] = true
	}
	if len(names) != 3 {
		t.Fatalf("admitted operation names = %d, want 3", len(names))
	}
}

// The endpoint is a name, never command text. Every case below is a shape a
// caller could send, and each one must be refused by the builder AND by the
// recogniser: a builder refuses to produce it, and an array that arrived from
// anywhere else is refused by the table.
func TestTheConnectionAdmissionsRefuseCommandText(t *testing.T) {
	cases := map[string][]string{
		"a hostname":                    {"connect", "lab.local:5555"},
		"an mDNS name":                  {"connect", "adb-1A2B3C._adb-tls-connect._tcp:5555"},
		"no port":                       {"connect", "192.168.1.109"},
		"a second colon":                {"connect", "192.168.1.109:5555:1"},
		"an IPv6 literal":               {"connect", "::1:5555"},
		"a port beyond the range":       {"connect", "192.168.1.109:70000"},
		"a port of zero":                {"connect", "192.168.1.109:0"},
		"a non-canonical port":          {"connect", "192.168.1.109:05555"},
		"an octet beyond the range":     {"connect", "256.168.1.109:5555"},
		"a non-canonical octet":         {"connect", "192.168.01.109:5555"},
		"a leading space":               {"connect", " 192.168.1.109:5555"},
		"a trailing space":              {"connect", "192.168.1.109:5555 "},
		"a shell metacharacter":         {"connect", "192.168.1.109:5555;id"},
		"a quote":                       {"connect", "192.168.1.109:5555'"},
		"a path":                        {"connect", "192.168.1.109:5555/adb"},
		"a flag in the endpoint":        {"connect", "-s"},
		"a shell-prefixed shape":        {"shell", "connect", endpoint},
		"a second command":              {"connect", endpoint, "&&", "kill-server"},
		"a bare disconnect":             {"disconnect"},
		"a disconnect for everything":   {"disconnect", "--all"},
		"a kill-server with extra text": {"kill-server", "please"},
		"a start-server with a serial":  {"start-server", "-s", "mock-device-alpha"},
		"a caller-authored command":     {"sh", "-c", "svc wifi enable"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := matchesHostAllowlist(args); ok {
				t.Fatalf("%v was admitted by the host recogniser", args)
			}
			if _, ok := matchesAllowlist(args); ok {
				t.Fatalf("%v was admitted by the combined table", args)
			}
		})
	}

	// And the builders refuse to build them in the first place: the table refusing
	// an array is the second gate, not the first.
	for _, bad := range []string{"lab.local:5555", "192.168.1.109", "192.168.1.109:70000", "192.168.1.109:05555", " 192.168.1.109:5555", "192.168.1.109:5555;id"} {
		if _, err := ConnectArgv(bad); err == nil {
			t.Fatalf("ConnectArgv(%q) built an array for an endpoint that is not one", bad)
		}
	}
}

// D6: there is no `disconnect`. A targeted disconnect removes the host's transport
// record while leaving the device itself reachable, so a recovery proven against it
// proves nothing about the failures that actually occur, and this card's recovery
// path is kill-server + start-server followed by re-establishing each endpoint.
//
// This test exists so the removal cannot be quietly reverted: an admission that is
// gone with no assertion pinning its absence is an admission that comes back.
func TestThereIsNoDisconnectAdmission(t *testing.T) {
	for name, args := range map[string][]string{
		"the targeted form":    {"disconnect", endpoint},
		"the bare form":        {"disconnect"},
		"the fleet-wide form":  {"disconnect", "--all"},
		"with a serial":        {"disconnect", "-s", "mock-device-alpha"},
		"a malformed endpoint": {"disconnect", "192.168.1.109:5555:1"},
	} {
		if opName, ok := matchesHostAllowlist(args); ok {
			t.Fatalf("%s %v was admitted by the host recogniser as %q; there is no disconnect admission (D6)", name, args, opName)
		}
		if opName, ok := matchesAllowlist(args); ok {
			t.Fatalf("%s %v was admitted by the combined table as %q; there is no disconnect admission (D6)", name, args, opName)
		}
	}
}

// The host admission and the device admissions cannot be confused in either
// direction. This is the separation the entry points rely on: the host runner
// asks the host recogniser alone, so a device array must not be admitted there,
// and no host array may be admitted by a device recogniser.
func TestTheHostAdmissionIsDisjointFromTheDeviceAdmissions(t *testing.T) {
	for name, args := range connectionArrays(t) {
		if opName, ok := matchesDeviceInputAllowlist(args); ok {
			t.Fatalf("%s array %v was admitted as the device input %q", name, args, opName)
		}
		if opName, ok := matchesReadOnlyAllowlist(args); ok {
			t.Fatalf("%s array %v was admitted as the read-only %q", name, args, opName)
		}
	}
	for _, deviceArgs := range inputArrays() {
		if opName, ok := matchesHostAllowlist(deviceArgs); ok {
			t.Fatalf("device input %v was admitted by the host recogniser as %q", deviceArgs, opName)
		}
	}
}

// The host entry point refuses everything that is not a connection array, and it
// refuses it before any execution. The adapter here has no runner at all, so an
// array that got as far as execution would report a missing runner instead: the
// error naming the allow-list is the proof that the gate came first.
func TestRunHostAllowlistedRefusesAnythingButAConnectionArray(t *testing.T) {
	adapter, err := NewAdapter(testExecutable, NewFakeRunner())
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	ctx := context.Background()

	for name, args := range map[string][]string{
		"a device input":            {"shell", "input", "tap", "540", "960"},
		"a read":                    {"shell", "wm", "size"},
		"a caller-authored command": {"shell", "-c", "id"},
		"a bare disconnect":         {"disconnect"},
		"a connect without a port":  {"connect", "192.168.1.109"},
	} {
		_, err := adapter.RunHostAllowlisted(ctx, args)
		if !errors.Is(err, ErrArgvNotAllowlisted) {
			t.Fatalf("%s: error = %v, want %v", name, err, ErrArgvNotAllowlisted)
		}
	}

	// An admitted array gets past the gate and fails later, for whatever reason the
	// runner has: this fake is unscripted, so it fails closed. What matters is that
	// the refusal is not the allow-list's.
	connect, err := ConnectArgv(endpoint)
	if err != nil {
		t.Fatalf("ConnectArgv: %v", err)
	}
	if _, err := adapter.RunHostAllowlisted(ctx, connect); errors.Is(err, ErrArgvNotAllowlisted) {
		t.Fatalf("an admitted connection array was refused by the allow-list: %v", err)
	}
}
