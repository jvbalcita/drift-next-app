package main

import (
	"fmt"

	"spikelocal/arc144/internal/annexb"
)

// spsProfileLevel reports the profile-level-id the bitstream itself declares,
// read out of the SPS, so the SDP fmtp line and the MSE contentType both state
// what the decoder will actually be handed instead of a guess.
func spsProfileLevel(aus []annexb.AccessUnit) (string, error) {
	for _, au := range aus {
		nals, err := annexb.NALs(au.Data)
		if err != nil {
			return "", err
		}
		for _, n := range nals {
			if n.Type != 7 {
				continue
			}
			// SPS RBSP: profile_idc, constraint flags, level_idc.
			if len(n.Payload) < 3 {
				return "", fmt.Errorf("SPS too short (%d bytes)", len(n.Payload))
			}
			return fmt.Sprintf("%02x%02x%02x", n.Payload[0], n.Payload[1], n.Payload[2]), nil
		}
	}
	return "", fmt.Errorf("no SPS found in %d access units", len(aus))
}
