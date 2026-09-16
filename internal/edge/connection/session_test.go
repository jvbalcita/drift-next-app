package connection_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/connection"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

func TestSessionTracksReconnectAndRefusesHighRiskWhileDisconnected(t *testing.T) {
	now := time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC)
	session := connection.NewSession(connection.SessionConfig{
		AgentID:     "agent-lab-1",
		TransportID: "usb:1",
		Protocol:    "drift-edge/1",
		HelperToken: "helper-token-obs-only",
	}, now)

	status := session.Status(now)
	if status.State != connection.RuntimeConnected {
		t.Fatalf("state = %q, want connected", status.State)
	}
	if status.HelperTokenIsLease || status.TransportIDIsLease {
		t.Fatal("helper token and transport id must never be control-plane leases")
	}

	if err := session.AuthorizeAction(action.RiskHigh, now); err != nil {
		t.Fatalf("connected high-risk authorize: %v", err)
	}

	session.MarkDisconnected(now.Add(time.Second), "network_loss")
	if err := session.AuthorizeAction(action.RiskHigh, now.Add(2*time.Second)); platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("disconnected high-risk code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
	if err := session.AuthorizeAction(action.RiskLow, now.Add(2*time.Second)); err != nil {
		t.Fatalf("disconnected low-risk should still authorize for observation: %v", err)
	}

	session.BeginReconnect(now.Add(3 * time.Second))
	if session.Status(now.Add(3*time.Second)).State != connection.RuntimeReconnecting {
		t.Fatalf("state = %q, want reconnecting", session.Status(now.Add(3*time.Second)).State)
	}

	if err := session.CompleteReconnect(connection.ReconnectEvidence{
		TransportID: "tcp:192.0.2.10:5555",
		Protocol:    "drift-edge/1",
	}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	status = session.Status(now.Add(4 * time.Second))
	if status.State != connection.RuntimeConnected || status.TransportID != "tcp:192.0.2.10:5555" {
		t.Fatalf("reconnected status = %#v", status)
	}
}

func TestSessionClassifiesAmbiguousDispatchAsIndeterminateWithoutBlindRetry(t *testing.T) {
	now := time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC)
	session := connection.NewSession(connection.SessionConfig{AgentID: "agent-1", TransportID: "usb:1", Protocol: "drift-edge/1"}, now)

	outcome, err := session.RecordDispatch("action-1", action.RiskMedium, now)
	if err != nil || outcome != connection.DispatchAccepted {
		t.Fatalf("dispatch = %q err=%v", outcome, err)
	}

	session.MarkDisconnected(now.Add(time.Second), "timeout")
	classified, err := session.ClassifyAfterDisconnect("action-1", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if classified != connection.DispatchIndeterminate {
		t.Fatalf("classified = %q, want indeterminate", classified)
	}

	if err := session.RetryBlind("action-1", now.Add(3*time.Second)); platformerrors.CodeOf(err) != platformerrors.CodeIndeterminateCompletion {
		t.Fatalf("blind retry code = %v, want indeterminate_completion", platformerrors.CodeOf(err))
	}

	if err := session.ResolveIndeterminate("action-1", connection.ResolutionFreshObservation, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.PendingIndeterminate() != 0 {
		t.Fatalf("pending indeterminate = %d, want 0", session.PendingIndeterminate())
	}
}

func TestSessionRejectsDispatchAfterDisconnectWithoutRaceWindow(t *testing.T) {
	now := time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC)
	session := connection.NewSession(connection.SessionConfig{AgentID: "agent-1", TransportID: "usb:1", Protocol: "drift-edge/1"}, now)
	session.MarkDisconnected(now.Add(time.Second), "network_loss")
	_, err := session.RecordDispatch("late-action", action.RiskLow, now.Add(2*time.Second))
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("dispatch while disconnected code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
}

func TestSessionRejectsStalePolicyWhileDisconnected(t *testing.T) {
	now := time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC)
	session := connection.NewSession(connection.SessionConfig{AgentID: "agent-1", TransportID: "usb:1", Protocol: "drift-edge/1", PolicyVersion: 2}, now)
	session.MarkDisconnected(now.Add(time.Second), "host_restart")
	err := session.AuthorizeActionWithPolicy(action.RiskLow, 1, now.Add(2*time.Second))
	if platformerrors.CodeOf(err) != platformerrors.CodePolicyDenied {
		t.Fatalf("stale policy code = %v, want policy_denied", platformerrors.CodeOf(err))
	}
}
