package events_test

import (
	"testing"
	"time"

	"drift.local/drift-next/internal/events"
	"drift.local/drift-next/internal/organizations"
)

func TestEventValidityRequiresSafeCorrelationEnvelope(t *testing.T) {
	event := events.Event{
		ID:            "event-1",
		Workspace:     organizations.WorkspaceID("workspace-1"),
		Name:          "device.observed",
		SchemaVersion: 1,
		CorrelationID: "correlation-1",
		ActorType:     events.ActorSystem,
		ActorID:       "system",
		Source:        events.SourceFake,
		ResourceType:  "device",
		ResourceID:    "device-1",
		PayloadJSON:   "{}",
		OccurredAt:    time.Unix(0, 0).UTC(),
	}
	if !event.Valid() {
		t.Fatal("epoch event with complete envelope was rejected")
	}
	event.CorrelationID = ""
	if event.Valid() {
		t.Fatal("event without correlation ID was accepted")
	}
	event.CorrelationID = "correlation-1"
	event.OccurredAt = time.Time{}
	if event.Valid() {
		t.Fatal("event without an occurrence time was accepted")
	}
}
