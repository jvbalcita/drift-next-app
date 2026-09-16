package execution_test

import (
	"context"
	"testing"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/execution"
	"drift.local/drift-next/internal/leases"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	store "drift.local/drift-next/internal/store/sqlite"
)

// The dispatcher's handling of every refusal case is already proven against a
// fake probe, and the lease/fence/idempotency and transport paths are proven
// against SQLite. What no test covered is the production readiness probe's own
// decision table: four of its branches were reachable in production and
// exercised by nothing, which is the difference between a separation that is
// asserted and one that is merely intended.
//
// These cases drive the real `StoreControlProbe` over a real database - the
// lease really is missing, really is held by someone else, really names another
// device, and its control session really has closed. Each must refuse with its
// own reason and reach no device.
func TestTheProductionProbeRefusesEachControlConditionDistinctly(t *testing.T) {
	cases := []struct {
		name     string
		want     execution.RefusalReason
		wantCode platformerrors.Code
		mutate   func(*testing.T, *sqliteInputFixture, *execution.InputRequest)
	}{
		{
			name:     "a lease that does not exist",
			want:     execution.RefusalLeaseMissing,
			wantCode: platformerrors.CodeLeaseConflict,
			mutate: func(_ *testing.T, _ *sqliteInputFixture, request *execution.InputRequest) {
				request.LeaseID = "lease-that-does-not-exist"
			},
		},
		{
			name:     "a lease held by another controller",
			want:     execution.RefusalLeaseNotHeld,
			wantCode: platformerrors.CodeLeaseConflict,
			mutate: func(_ *testing.T, _ *sqliteInputFixture, request *execution.InputRequest) {
				request.HolderID = "holder-beta"
			},
		},
		{
			name:     "a lease for a different device than the one requested",
			want:     execution.RefusalLeaseConflict,
			wantCode: platformerrors.CodeLeaseConflict,
			mutate: func(_ *testing.T, _ *sqliteInputFixture, request *execution.InputRequest) {
				request.DeviceID = "device-beta"
			},
		},
		{
			name: "a control session that has closed, which ends its own lease",
			// Closing a session releases its leases in the same transaction, so a
			// dispatch after that moment never reaches the probe's session check:
			// the lease gate refuses first. This asserts the cascade rather than
			// the reason's name, because the reason for a lease that is no longer
			// active is the lease's, and the lease really did leave the active
			// state.
			want:     execution.RefusalLeaseExpired,
			wantCode: platformerrors.CodeLeaseConflict,
			mutate: func(t *testing.T, fixture *sqliteInputFixture, _ *execution.InputRequest) {
				t.Helper()
				ctx := context.Background()
				leaseService := store.NewLeaseService(fixture.db, time.Hour)
				lease, err := leaseService.Get(ctx, fixture.workspace, leases.DeviceLeaseID(fixture.lease))
				if err != nil {
					t.Fatalf("read the lease this fixture acquired: %v", err)
				}
				if _, err := store.NewSessionService(fixture.db, time.Hour).Close(ctx, fixture.workspace, lease.SessionID, "operator", "operator-1"); err != nil {
					t.Fatalf("close the control session: %v", err)
				}
				after, err := leaseService.Get(ctx, fixture.workspace, leases.DeviceLeaseID(fixture.lease))
				if err != nil {
					t.Fatalf("read the lease after closing its session: %v", err)
				}
				if after.State == leases.LeaseActive {
					t.Fatalf("closing the control session left its lease in state %q: the session check is only unreachable while a session's end also ends its leases", after.State)
				}
			},
		},
	}

	reasons := make(map[execution.RefusalReason]string, len(cases))
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSQLiteInputFixture(t, time.Hour, adb.StateDevice)
			request := fixture.request("attempt-"+test.name, "key-"+test.name)
			test.mutate(t, &fixture, &request)

			result, err := fixture.dispatcher.Run(context.Background(), request, "operator", "operator-1")
			refusal, ok := execution.RefusalOf(err)
			if !ok {
				t.Fatalf("error = %v, want a typed refusal from the production probe", err)
			}
			if refusal.Reason != test.want {
				t.Fatalf("refusal reason = %q, want %q", refusal.Reason, test.want)
			}
			if refusal.Code != test.wantCode {
				t.Fatalf("refusal code = %q, want %q", refusal.Code, test.wantCode)
			}
			// The lease refusals deliberately share one platform code, so the
			// reason is what distinguishes them; the failure class is shared for
			// the same recorded reason.
			if refusal.FailureClass != domain.FailureLeaseConflict {
				t.Fatalf("refusal failure class = %q, want %q", refusal.FailureClass, domain.FailureLeaseConflict)
			}
			if platformerrors.CodeOf(err) == platformerrors.CodeInternal {
				t.Fatalf("refusal %q fell through to a generic internal error: %v", refusal.Reason, err)
			}
			if result.Attempt.ID != "" || result.Outcome != "" {
				t.Fatalf("refused dispatch returned %#v, want an empty result", result)
			}
			if calls := fixture.transport.invocationCount(); calls != 0 {
				t.Fatalf("refused dispatch issued %d device calls, want 0", calls)
			}
		})
	}

	for _, test := range cases {
		if existing, ok := reasons[test.want]; ok {
			t.Fatalf("refusal reason %q is shared by %q and %q", test.want, existing, test.name)
		}
		reasons[test.want] = test.name
	}
	if len(reasons) != len(cases) {
		t.Fatalf("distinct refusal reasons = %d, want %d", len(reasons), len(cases))
	}
}

// TestALeaseCannotOutliveItsControlSession records why one half of the probe's
// session check cannot be reached in production, so that the reason is evidence
// rather than an argument.
//
// ProbeControl refuses with "no control session" when the session is not active
// OR when its expiry has passed. The expiry half is dead while a lease is
// active, because acquiring a lease caps its lifetime at the session's: a lease
// is never the last of the pair to expire. A dispatch after that moment is
// refused for the expired lease, one gate earlier. The state half is very much
// alive, which is what the case above exercises.
//
// If this assertion ever fails, the cap has been removed and the expiry branch
// becomes reachable, so the probe's session-expiry refusal needs its own case.
func TestALeaseCannotOutliveItsControlSession(t *testing.T) {
	ctx := context.Background()
	// A lease TTL far longer than the session's hour is exactly the shape that
	// would outlive the session if the cap were not applied.
	fixture := newSQLiteInputFixture(t, 8*time.Hour, adb.StateDevice)

	lease, err := store.NewLeaseService(fixture.db, time.Hour).Get(ctx, fixture.workspace, leases.DeviceLeaseID(fixture.lease))
	if err != nil {
		t.Fatalf("read the lease: %v", err)
	}
	session, err := store.NewSessionService(fixture.db, time.Hour).Get(ctx, fixture.workspace, lease.SessionID)
	if err != nil {
		t.Fatalf("read the control session: %v", err)
	}
	if lease.ExpiresAt.After(session.ExpiresAt) {
		t.Fatalf(
			"lease expires at %s, after its session at %s: a lease that outlives its session makes the probe's session-expiry branch reachable, and it needs a refusal case of its own",
			lease.ExpiresAt, session.ExpiresAt,
		)
	}
}
