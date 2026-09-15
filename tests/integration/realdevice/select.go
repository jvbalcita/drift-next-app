package realdevice

import (
	"fmt"
	"strings"
)

// EnvSerial is the explicit serial required when more than one usable
// transport is attached. A target is never inferred from list order.
const EnvSerial = "DRIFT_REALDEVICE_SERIAL"

// Selection is the operator-chosen serial for an attended run.
type Selection struct {
	Serial string
	Skip   string
}

// SelectSerial chooses exactly one attached serial. Zero attached devices
// skip. Two or more require an explicit requested serial that matches one
// attached transport. A single attached device may be used without EnvSerial.
func SelectSerial(attached []string, requested string) (Selection, error) {
	requested = strings.TrimSpace(requested)
	if len(attached) == 0 {
		return Selection{Skip: "adb devices shows no device state"}, nil
	}
	if len(attached) > 1 && requested == "" {
		return Selection{}, fmt.Errorf("multiple attached devices; set %s to select one", EnvSerial)
	}
	serial := requested
	if serial == "" {
		serial = attached[0]
	}
	matched := 0
	for _, candidate := range attached {
		if candidate == serial {
			matched++
		}
	}
	if matched == 0 {
		return Selection{}, fmt.Errorf("%s=%q is not among attached devices", EnvSerial, serial)
	}
	if matched > 1 {
		return Selection{}, fmt.Errorf("%s matched %d attached transports; the target is ambiguous", EnvSerial, matched)
	}
	return Selection{Serial: serial}, nil
}
