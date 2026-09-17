package endpoints_test

import (
	"testing"

	"drift.local/drift-next/internal/endpoints"
)

// The transport is read from the address the observation itself carries. A
// device reached over TCP is named by the address it answers on; a USB transport
// is named by a hardware serial that carries no address at all. Neither case is
// decided by matching a magic word in a string.
func TestTransportOfReadsTheObservationsOwnAddress(t *testing.T) {
	for _, test := range []struct {
		name   string
		serial string
		host   string
		port   uint16
		want   endpoints.Transport
	}{
		{name: "usb hardware serial", serial: "R5CT30ABCD", want: endpoints.TransportUSB},
		{name: "usb emulator serial", serial: "emulator-5554", want: endpoints.TransportUSB},
		{name: "tcp transport named by the address it answers on", serial: "192.168.1.106:5556", want: endpoints.TransportTCP},
		{name: "tcp transport with an explicit address", serial: "SER-1", host: "192.168.1.106", port: 5556, want: endpoints.TransportTCP},
		{name: "tcp transport with a host and no port", serial: "SER-1", host: "192.168.1.106", want: endpoints.TransportTCP},
		{name: "tcp transport with a port and no host", serial: "SER-1", port: 5555, want: endpoints.TransportTCP},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := endpoints.TransportOf(test.serial, test.host, test.port); got != test.want {
				t.Fatalf("TransportOf(%q, %q, %d) = %q, want %q", test.serial, test.host, test.port, got, test.want)
			}
		})
	}
}

// SplitAddress reads a host:port and nothing else. A serial that is not one is
// not an address, and reading a host or a port out of it would put a transport
// on a record that has none.
func TestSplitAddressOnlyReadsATransportAddress(t *testing.T) {
	for _, serial := range []string{
		"",
		"R5CT30ABCD",
		"emulator-5554",
		"192.168.1.106:",
		":5556",
		"192.168.1.106:55a6",
		"host:port",
		"192.168.1.106:99999",
	} {
		if host, port, ok := endpoints.SplitAddress(serial); ok {
			t.Fatalf("SplitAddress(%q) = (%q, %d, true); a serial that is not a host:port is not an address", serial, host, port)
		}
	}

	host, port, ok := endpoints.SplitAddress("192.168.1.106:5556")
	if !ok || host != "192.168.1.106" || port != 5556 {
		t.Fatalf("SplitAddress(192.168.1.106:5556) = (%q, %d, %v); want the host and port it names", host, port, ok)
	}
}

// A transport that was never observed is the zero value and reports itself as
// invalid, so a reader can tell "not observed" from a transport without
// inventing one.
func TestTransportValidityDistinguishesUnobserved(t *testing.T) {
	for _, observed := range []endpoints.Transport{endpoints.TransportUSB, endpoints.TransportTCP} {
		if !observed.Valid() {
			t.Fatalf("%q.Valid() = false, want true", observed)
		}
	}
	if endpoints.TransportUnspecified.Valid() {
		t.Fatal("the unspecified transport reported itself as valid; an unobserved transport is not a transport")
	}
}
