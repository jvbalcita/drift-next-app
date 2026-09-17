package lab_test

import (
	"context"
	"errors"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
)

// fakeEnumerator stands in for the attached-transport reader. It is a pointer
// with nothing wrapped around it, so an accessor that re-wraps what it was given
// fails the identity assertion below.
type fakeEnumerator struct {
	calls int
}

func (f *fakeEnumerator) Enumerate(_ context.Context) ([]adb.DiscoveredDevice, error) {
	f.calls++
	return nil, nil
}

// TestLabModeExposesTheEnumeratorItWasGiven: the composition root builds a restart
// and an activation over this reader, and what it gets back must be the value the
// service was built with — never a wrapper, because both operations compare the
// fleet before and after through it.
func TestLabModeExposesTheEnumeratorItWasGiven(t *testing.T) {
	fake := newFakeAdapter()
	enumerator := &fakeEnumerator{}

	service, err := lab.NewService(lab.WithLabAdapters(fake, fake), lab.WithLabEnumerator(enumerator))
	if err != nil {
		t.Fatalf("building a lab service with an enumerator failed: %v", err)
	}

	got := service.Enumerator()
	if got == nil {
		t.Fatal("a service built with an enumerator exposes none")
	}
	exposed, ok := got.(*fakeEnumerator)
	if !ok {
		t.Fatalf("the exposed enumerator is %T, not the one this service was built with", got)
	}
	if exposed != enumerator {
		t.Fatal("the exposed enumerator is a different value from the one this service was built with")
	}
}

// TestNoEnumeratorWithoutTheExplicitOptIn: no way to read the attached fleet, no
// restart and no activation mounted — both would be reporting a before and after
// they cannot actually read.
func TestNoEnumeratorWithoutTheExplicitOptIn(t *testing.T) {
	fake := newFakeAdapter()

	service, err := lab.NewService(lab.WithLabAdapters(fake, fake))
	if err != nil {
		t.Fatalf("building a lab service failed: %v", err)
	}
	if got := service.Enumerator(); got != nil {
		t.Fatalf("a service built without the enumerator opt-in exposed %T; no reader, no restart", got)
	}
}

// TestTheEnumeratorOptInRefusesNothing: an opt-in that binds nothing is a bug at
// the composition root, so it fails closed when the service is built rather than
// leaving a service that looks ready to re-establish endpoints and is not.
func TestTheEnumeratorOptInRefusesNothing(t *testing.T) {
	fake := newFakeAdapter()

	_, err := lab.NewService(lab.WithLabAdapters(fake, fake), lab.WithLabEnumerator(nil))
	if !errors.Is(err, lab.ErrEnumeratorRequired) {
		t.Fatalf("an enumerator opt-in binding nothing returned %v, want %v", err, lab.ErrEnumeratorRequired)
	}
}
