package transportconnect_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
)

func TestDeviceContractPreservesExistingFieldNumbersAndAddsWorkspaceContext(t *testing.T) {
	fields := driftv1.File_drift_v1_device_proto.Messages().ByName("Device").Fields()
	for number, want := range map[protoreflect.FieldNumber]string{
		1:  "id",
		2:  "display_name",
		3:  "agent_id",
		4:  "status",
		5:  "platform_version",
		6:  "battery_percent",
		7:  "latency_ms",
		8:  "last_seen_at",
		9:  "workspace",
		10: "endpoint_id",
		11: "row_version",
	} {
		field := fields.ByNumber(number)
		if field == nil || string(field.Name()) != want {
			t.Fatalf("Device field %d = %v, want %q", number, field, want)
		}
	}
}

func TestAssistanceContractHasNoExecutionOrCredentialAuthorityFields(t *testing.T) {
	fields := driftv1.File_drift_v1_assistance_proto.Messages().ByName("ProposeRequest").Fields()
	for _, forbidden := range []string{"action", "command", "lease", "fencing_token", "token", "credential", "filesystem_path"} {
		if field := fields.ByName(protoreflect.Name(forbidden)); field != nil {
			t.Fatalf("ProposeRequest must not expose %q authority", forbidden)
		}
	}
}
