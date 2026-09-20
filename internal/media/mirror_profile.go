package media

import (
	"fmt"
	"strconv"
	"strings"

	"drift.local/drift-next/internal/edge/adb"
)

// The plane's live-stream budget (ARC-228).
//
// A device-session capacity bounds how many devices the plane mirrors at once, and
// that bound alone does not say how many tile pictures the console's grid may hold:
// every live stream is encoded bytes on ONE path, so a plane that carried its whole
// capacity at its most expensive quality would be a plane whose pictures are all
// late. This file is where that second bound is stated.
//
// Two facts make it up:
//
//   - What ONE stream at a preview level costs the transport. It is the level's own
//     bit rate cap, read from the device adapter's table rather than spelled a
//     second time here, because the number IS the encoder's bound: a plane that
//     budgeted against a copy of it would keep budgeting after the cap moved, and
//     would size a grid against a stream nobody is producing.
//   - How much of the transport this deployment has measured. That is a fact about
//     this host's path and not about the plane, so it is a deployment input
//     (EnvTransportBudgetKbps) whose documented default is the aggregate measured
//     on this fleet.
//
// The spend is therefore `capacity x bitrate(level)`, and it is compared against
// the stated budget so that choosing a more expensive quality REDUCES how many
// tiles may be live rather than oversubscribing the path.

// DefaultTransportBudgetKbps is the aggregate live-stream budget a deployment that
// states none gets.
//
// It is the largest aggregate this fleet was MEASURED carrying: four concurrent
// live sessions at the unbounded native profile put 32.0 Mbps on the transport and
// every one of them held 30 fps for the whole window, while a fifth session could
// not be opened at all (see docs/operations/live-stream-concurrency.md for the
// method, the conditions and the per-stream counts).
//
// It is deliberately NOT the hub's bulk-transfer figure. A path that moves 78 Mbps
// of four fat adb pulls is a different demand from four live encodes - a stream's
// cost is in MOTION, and a bounded tile costs a fraction of a bulk transfer's
// bandwidth - so budgeting video against a file-transfer measurement is how a
// transport gets oversubscribed. What lies ABOVE this number on this fleet is
// unmeasured, because the fleet could not open a fifth session to find out; and a
// default is a bound this product stands behind rather than a guess about a knee
// nobody measured.
const DefaultTransportBudgetKbps = 32000

const (
	// EnvTransportBudgetKbps is the aggregate live-stream budget this deployment
	// states for the transport its streams share, in kilobits per second.
	//
	// It is a deployment input because it is a measurement of a path rather than a
	// property of the plane: two hosts carrying the same fleet over a hub and over
	// Wi-Fi do not have the same transport, and only the deployment knows which one
	// it is on. DefaultTransportBudgetKbps applies when it is unset, and the
	// plane's startup line states which of the two is in force.
	EnvTransportBudgetKbps = "DRIFT_MIRROR_TRANSPORT_BUDGET_KBPS"
)

// PreviewBitrateKbps reports what one live stream at a preview level costs the
// transport, in kilobits per second, and false for a level this product does not
// have.
//
// The cost is the level's own bit rate cap - the number the device's encoder is
// held to - read from the adapter's table. It is rounded UP to whole kilobits and
// documented as such rather than rounded to nearest, because a budget that
// understated a stream's cost would let the plane carry one more tile than the path
// holds, which is the oversubscription this bound exists to prevent.
func PreviewBitrateKbps(quality MirrorPreviewQuality) (int, bool) {
	bitsPerSecond, known := adb.MirrorPreviewBitRate(string(quality))
	if !known || bitsPerSecond <= 0 {
		return 0, false
	}
	return (bitsPerSecond + 999) / 1000, true
}

// ProfileBitrate is one preview level and what a stream at it costs.
type ProfileBitrate struct {
	// Quality is the level.
	Quality MirrorPreviewQuality
	// BitrateKbps is what one live stream at it costs the transport.
	BitrateKbps int
}

// String renders one level's cost as `medium=1200kbps`.
func (p ProfileBitrate) String() string {
	return fmt.Sprintf("%s=%dkbps", p.Quality, p.BitrateKbps)
}

// PreviewBitrates reports every level this plane can price with what one stream at
// it costs, in the order a surface offers them.
//
// It exists for the startup line: an operator reading a deployment back needs the
// per-level cost the plane is actually using and not only the level it selected, so
// that "the grid carries fewer tiles than it used to" is answerable from the frame
// - and so is what carrying more would cost.
func PreviewBitrates() []ProfileBitrate {
	out := make([]ProfileBitrate, 0, len(adb.MirrorPreviewLevels))
	for _, level := range adb.MirrorPreviewLevels {
		bitrate, known := PreviewBitrateKbps(MirrorPreviewQuality(level))
		if !known {
			continue
		}
		out = append(out, ProfileBitrate{Quality: MirrorPreviewQuality(level), BitrateKbps: bitrate})
	}
	return out
}

// PreviewBitrateList renders every priced level as `low=500kbps, medium=1200kbps`
// and so on, which is what the startup line carries.
func PreviewBitrateList() string {
	priced := PreviewBitrates()
	parts := make([]string, 0, len(priced))
	for _, profile := range priced {
		parts = append(parts, profile.String())
	}
	return strings.Join(parts, ", ")
}

// TransportBudgetFromEnv reads how much of the transport this deployment states
// its live streams may spend.
//
// It fails where the deployment is configured, like the capacity beside it: a
// budget that is not a positive whole number of kilobits per second is refused
// here, so a mistyped bound is a startup line rather than a fleet of tiles that
// behave inexplicably. An unset input is not an error - it answers with the
// documented default, which the plane's own startup line then states.
func TransportBudgetFromEnv(lookup EnvLookup) (int, error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	if raw, ok := lookup(EnvTransportBudgetKbps); ok && strings.TrimSpace(raw) != "" {
		budget, parseErr := strconv.Atoi(strings.TrimSpace(raw))
		if parseErr != nil || budget <= 0 {
			return 0, fmt.Errorf("%s must be a positive whole number of kilobits per second, and this deployment configured %q",
				EnvTransportBudgetKbps, strings.TrimSpace(raw))
		}
		return budget, nil
	}
	return DefaultTransportBudgetKbps, nil
}

// tileAllowanceKbps derives how many streams of this cost a budget carries, and
// never a negative count.
//
// A stream that costs nothing is not a stream this plane can price, so a level
// with no stated cost answers zero places rather than dividing by it.
func tileAllowanceKbps(budgetKbps, bitrateKbps int) int {
	if bitrateKbps <= 0 || budgetKbps <= 0 {
		return 0
	}
	return budgetKbps / bitrateKbps
}
