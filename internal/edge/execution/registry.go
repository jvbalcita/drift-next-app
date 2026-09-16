// Package execution binds one authorized control-plane attempt to a serialized
// per-device actor.
//
// Observation stays read-only: the observation adapter never injects input. A
// mutating catalog kind executes only through the five narrow, parameterised
// primitives in this package — tap, swipe, typed text by reference, key event
// and app launch. Each takes typed parameters, honours cancellation and its own
// deadline, validates a coordinate against the render space carried with it
// (the `wm size` OVERRIDE, never the physical panel size), cross-checks that
// frame against the render size the device actually reports — refusing on a
// mismatch, an unreadable device, or a reading too old to trust — and executes
// an argument array this package built. There is no shell, no exec, and no
// caller-authored command text: a generic or incomplete payload has no
// representation here. These primitives carry no authority of their own — the
// lease, fencing, policy, control-session and emergency-stop kernel authorizes;
// they only execute what it authorized.
package execution

import (
	"context"
	"sync"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/edge/actors"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/runner"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

type deviceActor struct {
	serial string
	actor  *actors.Actor
}

// Registry owns one actor per device and runs authorized observation through
// the lab transport for that device's current endpoint serial. The serial is
// named on every capture, so the lab boundary authorizes it against the
// attached transports instead of against session state.
type Registry struct {
	lab     lab.ObservationCapturer
	db      *store.DB
	control runner.ControlPlane
	mu      sync.Mutex
	actors  map[string]deviceActor
}

func NewRegistry(capturer lab.ObservationCapturer, db *store.DB) *Registry {
	if capturer == nil || db == nil {
		return nil
	}
	return &Registry{lab: capturer, db: db, control: store.NewActionService(db), actors: make(map[string]deviceActor)}
}

func (r *Registry) Run(ctx context.Context, intent action.Intent, actorType, actorID string) (action.Result, error) {
	if r == nil || r.lab == nil || r.db == nil || r.control == nil {
		return action.Result{}, runner.ErrUnavailable
	}
	serial, err := r.currentSerial(ctx, intent.Workspace, intent.DeviceID)
	if err != nil {
		return action.Result{}, err
	}
	actor, err := r.actorFor(intent.DeviceID, serial, actorID)
	if err != nil {
		return action.Result{}, err
	}
	return runner.New(r.control, actor).Run(ctx, intent, actorType, actorID)
}

func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var first error
	for id, bound := range r.actors {
		if err := bound.actor.Close(); err != nil && first == nil {
			first = err
		}
		delete(r.actors, id)
	}
	return first
}

// EndpointSerialResolver resolves a device to the serial of the one transport
// endpoint that is current for it.
//
// It is the application-boundary resolution ADR-0012 deliberately kept out of
// the transport contract: an RPC names a device, and a transport that resolved
// the serial itself would be inventing a target the caller never named. The
// boundary resolves it here, against the same registry the observation path
// uses, so both reach the same device for the same reason.
type EndpointSerialResolver interface {
	CurrentSerial(ctx context.Context, workspace, deviceID string) (string, error)
}

// CurrentSerial resolves the serial of a device's single current transport
// endpoint. It is the exported form of the lookup Run already performs, so the
// composition root resolves a device the same way this registry does instead of
// reimplementing the rule and drifting from it.
//
// It refuses rather than guesses: no current endpoint, more than one current
// endpoint, and a current endpoint with no serial are all failures that name no
// serial. A guess here would send an operator's input to whatever device
// happened to be reachable.
func (r *Registry) CurrentSerial(ctx context.Context, workspace, deviceID string) (string, error) {
	if r == nil || r.db == nil {
		return "", runner.ErrUnavailable
	}
	return r.currentSerial(ctx, workspace, deviceID)
}

func (r *Registry) currentSerial(ctx context.Context, workspace, deviceID string) (string, error) {
	endpoints, err := store.NewEndpointRepository(r.db).ListCurrent(ctx, organizations.WorkspaceID(workspace), devices.DeviceID(deviceID))
	if err != nil {
		return "", err
	}
	if len(endpoints) == 0 {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "device has no current transport endpoint")
	}
	if len(endpoints) > 1 {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "device has more than one current transport endpoint")
	}
	serial := endpoints[0].Serial
	if serial == "" {
		return "", platformerrors.New(platformerrors.CodePreconditionFailed, "current endpoint has no serial")
	}
	return serial, nil
}

func (r *Registry) actorFor(deviceID, serial, operatorID string) (*actors.Actor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if bound, ok := r.actors[deviceID]; ok {
		if bound.serial == serial {
			return bound.actor, nil
		}
		_ = bound.actor.Close()
		delete(r.actors, deviceID)
	}
	observation, err := lab.NewObservationAdapter(r.lab, serial, operatorID)
	if err != nil {
		return nil, err
	}
	actor, err := actors.New(deviceID, observation, 8)
	if err != nil {
		return nil, err
	}
	r.actors[deviceID] = deviceActor{serial: serial, actor: actor}
	return actor, nil
}
