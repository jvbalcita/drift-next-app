package realdevice

import "testing"

func TestSelectSerialSkipsWhenNoDeviceIsAttached(t *testing.T) {
	selection, err := SelectSerial(nil, "")
	if err != nil {
		t.Fatalf("SelectSerial() error = %v", err)
	}
	if selection.Skip == "" || selection.Serial != "" {
		t.Fatalf("SelectSerial() = %+v, want a skip with no serial", selection)
	}
}

func TestSelectSerialUsesTheOnlyAttachedDevice(t *testing.T) {
	selection, err := SelectSerial([]string{"SERIAL-A"}, "")
	if err != nil {
		t.Fatalf("SelectSerial() error = %v", err)
	}
	if selection.Skip != "" || selection.Serial != "SERIAL-A" {
		t.Fatalf("SelectSerial() = %+v, want SERIAL-A", selection)
	}
}

func TestSelectSerialRequiresAnExplicitChoiceWhenMultipleDevicesAreAttached(t *testing.T) {
	if _, err := SelectSerial([]string{"SERIAL-A", "SERIAL-B"}, ""); err == nil {
		t.Fatal("SelectSerial() error = nil, want an explicit-selection error")
	}
	selection, err := SelectSerial([]string{"SERIAL-A", "SERIAL-B"}, "SERIAL-B")
	if err != nil {
		t.Fatalf("SelectSerial() error = %v", err)
	}
	if selection.Serial != "SERIAL-B" {
		t.Fatalf("SelectSerial() serial = %q, want SERIAL-B", selection.Serial)
	}
}

func TestSelectSerialRejectsASerialThatIsNotAttached(t *testing.T) {
	if _, err := SelectSerial([]string{"SERIAL-A"}, "SERIAL-B"); err == nil {
		t.Fatal("SelectSerial() error = nil, want a missing-serial error")
	}
}

func TestSelectSerialRejectsAnAmbiguousRequestedSerial(t *testing.T) {
	if _, err := SelectSerial([]string{"SERIAL-A", "SERIAL-A"}, "SERIAL-A"); err == nil {
		t.Fatal("SelectSerial() error = nil, want an ambiguous-target error")
	}
}
