package adb

import (
	"context"
	"errors"
	"testing"
)

// D3: the tcpip admission follows slice 1's shape. It is the device transport-mode
// change, so it is admitted on the serial-bound table and is UNREACHABLE from the
// serial-free host path — the two things this file exists to pin.
func TestTheTcpipAdmissionIsRecognisedUnderItsOwnName(t *testing.T) {
	argv, err := TcpipArgv(5556)
	if err != nil {
		t.Fatalf("TcpipArgv(5556) = %v", err)
	}
	if len(argv) != 2 || argv[0] != "tcpip" || argv[1] != "5556" {
		t.Fatalf("TcpipArgv(5556) = %v, want [tcpip 5556]", argv)
	}
	name, ok := matchesTransportAllowlist(argv)
	if !ok {
		t.Fatalf("%v is not admitted by the transport recogniser", argv)
	}
	if name != "tcpip" {
		t.Fatalf("op name = %q, want %q", name, "tcpip")
	}
	if name, ok := matchesAllowlist(argv); !ok || name != "tcpip" {
		t.Fatalf("the combined table = %q, %t; want tcpip, true", name, ok)
	}
}

// A host-level array is reached through the serial-free entry point. tcpip must NOT be
// admitted there: it would change a device's transport mode without naming a device,
// which is a command whose subject is whoever happens to be attached.
func TestTcpipIsNotAdmittedOnTheSerialFreeHostPath(t *testing.T) {
	argv, err := TcpipArgv(5556)
	if err != nil {
		t.Fatalf("TcpipArgv(5556) = %v", err)
	}
	if name, ok := matchesHostAllowlist(argv); ok {
		t.Fatalf("%v was admitted by the host recogniser as %q; a transport-mode change must not be reachable without a serial", argv, name)
	}

	adapter, err := NewAdapter(testExecutable, NewFakeRunner())
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if _, err := adapter.RunHostAllowlisted(context.Background(), argv); !errors.Is(err, ErrArgvNotAllowlisted) {
		t.Fatalf("RunHostAllowlisted(%v) = %v, want %v", argv, err, ErrArgvNotAllowlisted)
	}
}

// The port is a canonical bounded decimal, and nothing else can be expressed in that
// position. Every case below is refused by the recogniser AND by the combined table.
func TestTheTcpipAdmissionRefusesAnythingButACanonicalPort(t *testing.T) {
	for name, args := range map[string][]string{
		"a port of zero":          {"tcpip", "0"},
		"a port beyond the range": {"tcpip", "65536"},
		"a non-canonical port":    {"tcpip", "05556"},
		"a negative port":         {"tcpip", "-1"},
		"a missing port":          {"tcpip"},
		"a second argument":       {"tcpip", "5556", "extra"},
		"a serial inline":         {"tcpip", "-s", "mock-device-alpha"},
		"a shell-prefixed shape":  {"shell", "tcpip", "5556"},
		"a metacharacter":         {"tcpip", "5556;id"},
		"a space":                 {"tcpip", " 5556"},
		"a caller-authored cmd":   {"sh", "-c", "setprop service.adb.tcp.port 5556"},
	} {
		if opName, ok := matchesTransportAllowlist(args); ok {
			t.Fatalf("%s %v was admitted by the transport recogniser as %q", name, args, opName)
		}
		if opName, ok := matchesAllowlist(args); ok {
			t.Fatalf("%s %v was admitted by the combined table as %q", name, args, opName)
		}
	}
	if _, err := TcpipArgv(0); err == nil {
		t.Fatal("TcpipArgv(0) built an array; port 0 is not a port an operator means")
	}
}

// The transport admission is disjoint from the host admission in both directions, so
// neither path can run the other's command.
func TestTheTransportAndHostAdmissionsCannotBeConfused(t *testing.T) {
	tcpip, err := TcpipArgv(5556)
	if err != nil {
		t.Fatalf("TcpipArgv: %v", err)
	}
	for name, args := range connectionArrays(t) {
		if opName, ok := matchesTransportAllowlist(args); ok {
			t.Fatalf("the host array %s %v was admitted by the transport recogniser as %q", name, args, opName)
		}
	}
	if name, ok := matchesHostAllowlist(tcpip); ok {
		t.Fatalf("the tcpip array was admitted by the host recogniser as %q", name)
	}
}
