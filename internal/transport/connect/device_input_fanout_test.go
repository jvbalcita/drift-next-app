package transportconnect

import (
	"math"
	"testing"
	"time"

	"drift.local/drift-next/internal/edge/execution"
)

func TestFollowerFanoutMessageCarriesOnlyAdmissionTiming(t *testing.T) {
	report := execution.FollowerFanoutReport{
		RunID:              "fanout-1",
		TargetCount:        1,
		AcceptanceDuration: 17 * time.Millisecond,
		Followers: []execution.FollowerInputOutcome{{
			DeviceID:          "device-follower",
			Disposition:       execution.FollowerInputAccepted,
			Reason:            execution.FollowerReasonDelivered,
			AcceptanceLatency: 3 * time.Millisecond,
		}},
	}

	message := followerFanoutMessage(report)
	if message.GetAcceptanceDurationMs() != 17 {
		t.Fatalf("acceptance duration = %d ms, want 17", message.GetAcceptanceDurationMs())
	}
	if got := message.GetFollowers()[0].GetAcceptanceLatencyMs(); got != 3 {
		t.Fatalf("follower acceptance latency = %d ms, want 3", got)
	}
	if got := boundedDurationMillis((time.Duration(math.MaxUint32) + 1) * time.Millisecond); got != math.MaxUint32 {
		t.Fatalf("bounded duration = %d, want uint32 ceiling", got)
	}
}
