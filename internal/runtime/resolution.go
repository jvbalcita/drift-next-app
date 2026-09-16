package runtime

import (
	"context"
	"fmt"
)

// This file is the resolution path for an address served by a process this
// session did not start. Reporting that condition is not enough: an operator who
// is told only what is wrong, on a machine where the runtime will not start on
// that address, is stuck with no way forward. The two resolutions are the only
// two that exist, and both are the operator's to choose:
//
//   - adopt the running process, accepting that its revision cannot be verified;
//   - terminate it and start this session's own, so the session owns the
//     component and later restarts behave normally.
//
// Neither is ever chosen for the operator. Silent adoption is the stale-binary
// hazard - a process serving an old build against migrated state, reported as
// healthy. Silent termination could destroy a service the operator started on
// purpose. Visibility plus an explicit choice is what makes either one safe.

// stateAdopted means an operator chose to keep a listener this session did not
// start. It is deliberately neither stateReady nor stateExternal: the component
// is usable because the operator accepted it, and nothing here verified what it
// is running, so it must never read as health.
const stateAdopted componentState = "adopted"

// The resolution path is named wherever the blocked condition is reported, so
// the operator is never told only what is wrong.
const (
	resolveAdoptOffer     = "adopt it to keep running it, unverified"
	resolveTerminateOffer = "terminate it and start this session's own"
)

// unverifiedRevision is the one wording for what adoption cannot know, so the
// status surface and the log cannot drift apart into implying a check that never
// happened.
const unverifiedRevision = "its revision is UNVERIFIED: this session cannot tell which build it is running"

// adoptedListener is the external listener an operator accepted for a component.
type adoptedListener struct {
	address string
	holder  string
}

// ExternalComponent is one component whose address is served by a process this
// session did not start, as an operator surface needs to show it: what the
// component is, where it is served, and who is holding it. The holder travels
// with it because that is what the operator needs in order to decide, and asking
// for it later could name a different process than the one they were shown.
type ExternalComponent struct {
	Name    string
	Address string
	Holder  string
}

// ExternalComponents reports every component currently blocked by a listener
// this session does not own. It is the list an operator surface offers a choice
// about, and a component that is already adopted is not on it: the operator has
// made that decision, and re-offering it would be asking again.
func (s *Supervisor) ExternalComponents() []ExternalComponent {
	names := make([]string, 0, len(managedComponents))
	s.mu.Lock()
	for _, component := range managedComponents {
		if s.statuses[component.Name].State == stateExternal {
			names = append(names, component.Name)
		}
	}
	s.mu.Unlock()
	// Resolving the holder runs lsof, so it happens outside the lock.
	external := make([]ExternalComponent, 0, len(names))
	for _, name := range names {
		address := s.componentAddress(name)
		external = append(external, ExternalComponent{Name: name, Address: address, Holder: s.describeHolder(address)})
	}
	return external
}

// adoptedListenerFor reports the listener this session accepted for a component,
// and whether it is still there to be accepted. An adoption whose listener has
// gone is cleared rather than reported: the address is free again, so the next
// start should take it rather than believe the component is still served.
func (s *Supervisor) adoptedListenerFor(name string) (adoptedListener, bool) {
	s.mu.Lock()
	entry, adopted := s.adopted[name]
	s.mu.Unlock()
	if !adopted {
		return adoptedListener{}, false
	}
	if s.addressFree(entry.address) {
		s.mu.Lock()
		delete(s.adopted, name)
		s.mu.Unlock()
		s.appendLog(fmt.Sprintf("%s: the adopted process is no longer listening on %s; this session will start its own", name, entry.address))
		return adoptedListener{}, false
	}
	return entry, true
}

// adoptedDetail is the one rendering of an adopted component's state.
func adoptedDetail(entry adoptedListener) string {
	return fmt.Sprintf("Adopted %s serving %s; %s - nothing here checked it", entry.holder, entry.address, unverifiedRevision)
}

// externalDetail is the one rendering of the blocked condition, naming the
// address, the holder, and both resolutions.
func externalDetail(address, holder string) string {
	return fmt.Sprintf("%s is served by %s, a process this session did not start; %s, or %s",
		address, holder, resolveAdoptOffer, resolveTerminateOffer)
}

// Adopt accepts the listener on a component's address as this session's
// component, so Start All and Restart All stop being blocked by it.
//
// It starts nothing and stops nothing. It records the operator's decision and
// states what that decision cannot know: an adopted process may be running a
// different build than this source tree, and no check here can tell.
func (s *Supervisor) Adopt(name string) error {
	address := s.componentAddress(name)
	if address == "" {
		return s.failOperation(name, fmt.Errorf("%s has no local address, so there is no listener to adopt", name))
	}
	if s.owns(name) {
		return s.failOperation(name, fmt.Errorf("%s is already running in this session; there is no external listener to adopt", name))
	}
	if s.addressFree(address) {
		return s.failOperation(name, fmt.Errorf("nothing is listening on %s, so there is nothing to adopt; start %s instead", address, name))
	}
	entry := adoptedListener{address: address, holder: s.describeHolder(address)}
	s.mu.Lock()
	s.adopted[name] = entry
	s.mu.Unlock()
	detail := adoptedDetail(entry)
	s.setStatus(name, stateAdopted, detail)
	s.appendLog(name + ": " + detail + " - Start All and Restart All will leave it running")
	return nil
}

// Terminate stops the listener an operator chose to replace and then starts this
// session's own component, so the session owns it and later restarts work
// normally.
//
// The order is the point. The address is verified free after the holder stops
// and before anything is started: starting while the holder is still there would
// leave the foreign process serving while this session reported that it had
// replaced it.
func (s *Supervisor) Terminate(ctx context.Context, name string) error {
	address := s.componentAddress(name)
	if address == "" {
		return s.failOperation(name, fmt.Errorf("%s has no local address, so there is no listener to terminate", name))
	}
	if s.owns(name) {
		return s.failOperation(name, fmt.Errorf("%s is running in this session; stop it instead of terminating it", name))
	}
	if s.addressFree(address) {
		return s.failOperation(name, fmt.Errorf("nothing is listening on %s, so nothing was terminated; start %s instead", address, name))
	}
	listeners := s.ports.Listeners(address)
	if len(listeners) == 0 {
		return s.failOperation(name, fmt.Errorf("%s is held, but no process could be identified on it: stop it yourself and retry; this runtime will not start on an address it cannot free", address))
	}
	for _, entry := range listeners {
		if err := s.terminate(entry.PID); err != nil {
			return s.failOperation(name, fmt.Errorf("%s: could not stop %s holding %s: %w; stop it yourself and retry", name, describeListeners([]listener{entry}), address, err))
		}
		s.appendLog(fmt.Sprintf("%s: asked %s to stop, so this session can serve %s", name, describeListeners([]listener{entry}), address))
	}
	if !s.waitAddressFree(address) {
		return s.failOperation(name, fmt.Errorf("%s did not release %s within %s and may be ignoring the request; stop it yourself and retry (nothing was started on the address)", describeListeners(listeners), address, s.freeWindow))
	}
	s.mu.Lock()
	delete(s.adopted, name)
	s.mu.Unlock()
	s.appendLog(fmt.Sprintf("%s: %s is free; starting this session's own process", name, address))
	if err := s.StartComponent(ctx, name); err != nil {
		return err
	}
	s.appendLog(name + ": the external listener was terminated and this session's own process is serving " + address)
	return nil
}

// waitReadyFor waits for the readiness of a component this session started.
//
// An adopted component is not this session's process to probe: the operator
// accepted it as it already is, so there is nothing to wait for and nothing this
// session may claim about it. Waiting would either block Start All on a process
// this session does not own, or - worse - mark it ready on the strength of a
// response that verified nothing.
func (s *Supervisor) waitReadyFor(ctx context.Context, name, address string) error {
	if _, adopted := s.adoptedListenerFor(name); adopted {
		return nil
	}
	return s.waitReady(ctx, name, address)
}

// blockedStartError is the failure text for a start that cannot bind because a
// listener this session does not own holds the address. It names the address,
// the process, and both resolutions, so a refusal is never a dead end: the only
// instruction it used to give was one this session could not carry out.
func blockedStartError(name, address, holder string) error {
	return fmt.Errorf("%s: %s is already held by %s; this session did not start it, so another process would leave the stale one serving - %s, or %s",
		name, address, holder, resolveAdoptOffer, resolveTerminateOffer)
}

// stillHeldError is the failure text for a stop that did not free its address:
// what is true about the address, and what the operator can do next.
func stillHeldError(name, address, holder string) error {
	return fmt.Errorf("%s: %s is still held by %s; %s, or %s", name, address, holder, resolveAdoptOffer, resolveTerminateOffer)
}
