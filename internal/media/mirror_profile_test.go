package media

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A plane's tile budget is derived from BOTH bounds it carries - the session share
// and what the transport can carry at the preview level's per-stream cost - and
// these tests are that derivation from every side that matters:
//
//   - the shipped default is the deployment that was MEASURED, so the grid it sizes
//     is the grid this fleet actually carries;
//   - a more expensive level buys FEWER tiles rather than oversubscribing the path,
//     which is the whole reason the level's cost is on the wire;
//   - a level whose per-stream cost the budget cannot carry for as many tiles as the
//     session share would allow states the smaller tile count and says which numbers
//     produced it, rather than being allowed and then failing when a viewer
//     subscribes; and
//   - a budget below ONE stream's cost, and a reserve that is the whole capacity, are
//     refused where the plane is built, because neither is a bound any grid can be
//     derived from.

// TestTheShippedDefaultSizesTheGridToTheMeasuredDeployment pins the numbers a
// deployment that states nothing gets, because they are the ones in force on this
// fleet: four device sessions, one kept for the operator's own frame, the level an
// unstated preview setting resolves to, and the transport budget the fleet was
// measured carrying. The grid therefore holds three tiles - which is what the fleet
// carries today, now stated rather than assumed.
func TestTheShippedDefaultSizesTheGridToTheMeasuredDeployment(t *testing.T) {
	engine := newEngine(t, newFakeDialer(), MirrorEngineConfig{})

	if engine.Capacity() != DefaultMirrorSessionCapacity {
		t.Fatalf("the plane carries %d device session(s) with nothing stated, want the measured %d",
			engine.Capacity(), DefaultMirrorSessionCapacity)
	}
	if engine.OperatorReserve() != DefaultOperatorReserve {
		t.Fatalf("the plane keeps %d place(s) for the operator's own frame, want %d",
			engine.OperatorReserve(), DefaultOperatorReserve)
	}
	if engine.PreviewQuality() != DefaultPreviewQuality {
		t.Fatalf("the plane carries its ambient streams at %q with nothing stated, want the documented default (%q)",
			engine.PreviewQuality(), DefaultPreviewQuality)
	}
	wantBitrate, priced := PreviewBitrateKbps(DefaultPreviewQuality)
	if !priced {
		t.Fatalf("the plane's own default preview level (%q) is one it cannot price at all", DefaultPreviewQuality)
	}
	if engine.ProfileBitrateKbps() != wantBitrate {
		t.Fatalf("the plane prices a stream at the %s preview setting at %d kbps, want %d",
			engine.PreviewQuality(), engine.ProfileBitrateKbps(), wantBitrate)
	}
	if engine.TransportBudgetKbps() != DefaultTransportBudgetKbps {
		t.Fatalf("the plane states a transport budget of %d kbps, want the measured %d",
			engine.TransportBudgetKbps(), DefaultTransportBudgetKbps)
	}
	// The spend is the capacity at the level, stated rather than implied: a
	// deployment can read back whether the capacity it configured fits the path it
	// configured.
	if engine.TransportSpendKbps() != engine.Capacity()*engine.ProfileBitrateKbps() {
		t.Fatalf("the plane states a spend of %d kbps for %d session(s) at %d kbps, want %d",
			engine.TransportSpendKbps(), engine.Capacity(), engine.ProfileBitrateKbps(),
			engine.Capacity()*engine.ProfileBitrateKbps())
	}
	if engine.TransportSpendKbps() > engine.TransportBudgetKbps() {
		t.Fatalf("the shipped default states a spend of %d kbps against a budget of %d kbps: the default deployment does not fit the path it names",
			engine.TransportSpendKbps(), engine.TransportBudgetKbps())
	}
	if engine.TileAllowance() != 3 {
		t.Fatalf("the grid may hold %d tile picture(s) on the shipped default, want the 3 this fleet carries",
			engine.TileAllowance())
	}
	if engine.TileAllowanceBound() != BoundSessionShare {
		t.Fatalf("the shipped default's tile allowance is bounded by %q, want the session share to decide it (%q)",
			engine.TileAllowanceBound(), BoundSessionShare)
	}
	// The grid's allowance and the sessions it may hold have to agree on a plane
	// whose transport carries its whole capacity, or the console's bound and the
	// engine's bound are two different numbers.
	if engine.TileAllowance() != engine.AmbientCapacity() {
		t.Fatalf("the grid may hold %d session(s) but only %d tile picture(s): on this deployment the two bounds disagree",
			engine.AmbientCapacity(), engine.TileAllowance())
	}
}

// TestAMoreExpensivePreviewLevelBuysFewerTiles is the card's own rule that the
// preview level is a BUDGET and not a label: the same capacity and the same
// transport, at two levels, must allow two different numbers of live tiles - the
// cheaper one more.
func TestAMoreExpensivePreviewLevelBuysFewerTiles(t *testing.T) {
	// Eight sessions on a path that carries five native streams: the session share
	// is seven, so the transport is what decides.
	expensive := newEngine(t, newFakeDialer(), MirrorEngineConfig{
		MaxSessions: 8, OperatorReserve: 1, Preview: MirrorPreview{Quality: PreviewExtra}, TransportBudgetKbps: 32000,
	})
	cheaper := newEngine(t, newFakeDialer(), MirrorEngineConfig{
		MaxSessions: 8, OperatorReserve: 1, Preview: MirrorPreview{Quality: PreviewMedium}, TransportBudgetKbps: 32000,
	})

	// extra: 32000/6000 = 5 streams on the path, less the operator's 1 = 4 tiles.
	if expensive.TileAllowance() != 4 {
		t.Fatalf("the grid may hold %d tile picture(s) at the %s preview setting, want 4 (5 streams on the path less the operator's place)",
			expensive.TileAllowance(), PreviewExtra)
	}
	if expensive.TileAllowanceBound() != BoundTransportBudget {
		t.Fatalf("the %s preview setting's allowance is bounded by %q, want the transport budget (%q) to decide it",
			PreviewExtra, expensive.TileAllowanceBound(), BoundTransportBudget)
	}
	// medium: 32000/1200 = 26 streams on the path, so the session share (7) decides.
	if cheaper.TileAllowance() != 7 {
		t.Fatalf("the grid may hold %d tile picture(s) at the %s preview setting, want the session share (7)",
			cheaper.TileAllowance(), PreviewMedium)
	}
	if cheaper.TileAllowanceBound() != BoundSessionShare {
		t.Fatalf("the %s preview setting's allowance is bounded by %q, want the session share (%q) to decide it",
			PreviewMedium, cheaper.TileAllowanceBound(), BoundSessionShare)
	}
	if cheaper.TileAllowance() <= expensive.TileAllowance() {
		t.Fatalf("the cheaper preview setting allows %d tile(s) and the more expensive one %d: the preview level is not what the tile count is derived from",
			cheaper.TileAllowance(), expensive.TileAllowance())
	}
	// Neither setting changed the capacity: only the number of live pictures.
	if expensive.Capacity() != cheaper.Capacity() {
		t.Fatalf("two preview settings over the same deployment read different capacities (%d and %d)", expensive.Capacity(), cheaper.Capacity())
	}
}

// TestAPathThatCarriesFewerTilesThanTheShareStatesTheSmallerCount is the acceptance
// case "a profile whose per-tile cost exceeds the budget is refused or states the
// smaller tile count": here the count is smaller and not zero, the tile beyond it is
// refused with the numbers that produced it, and the operator's own frame - the
// place the budget was kept for - still opens.
func TestAPathThatCarriesFewerTilesThanTheShareStatesTheSmallerCount(t *testing.T) {
	// A 15 Mbps path carries two streams at the extra (native) level, and one of the
	// two places is the operator's own: the grid's share of the SESSIONS is seven, and
	// it is the transport that says one.
	engine := newEngine(t, newFakeDialer(), MirrorEngineConfig{
		MaxSessions: 8, OperatorReserve: 1, Preview: MirrorPreview{Quality: PreviewExtra}, TransportBudgetKbps: 15000,
	})
	if engine.AmbientCapacity() != 7 {
		t.Fatalf("the grid's session share is %d, want 7 - this case is about the transport bound and not the share", engine.AmbientCapacity())
	}
	if engine.TileAllowance() != 1 {
		t.Fatalf("a path that carries two streams at the %s preview setting allows the grid %d tile picture(s), want 1",
			PreviewExtra, engine.TileAllowance())
	}
	if engine.TileAllowanceBound() != BoundTransportBudget {
		t.Fatalf("the allowance is bounded by %q, want the transport budget (%q)",
			engine.TileAllowanceBound(), BoundTransportBudget)
	}

	if _, _, err := engine.StartViewer(context.Background(), "tile-00", "SERIAL-tile-00", PurposeAmbient, MirrorPreview{}); err != nil {
		t.Fatalf("the grid's first tile was refused on a path that carries it: %v", err)
	}
	_, _, err := engine.StartViewer(context.Background(), "tile-01", "SERIAL-tile-01", PurposeAmbient, MirrorPreview{})
	var capacity *SessionCapacityError
	if !errors.As(err, &capacity) {
		t.Fatalf("a second tile on a path that carries one was refused with %v, want a capacity refusal", err)
	}
	if capacity.Bound != BoundTransportBudget {
		t.Fatalf("the refusal blames %q, want the transport budget (%q)", capacity.Bound, BoundTransportBudget)
	}
	if capacity.AmbientShare != 1 || capacity.Capacity != 8 {
		t.Fatalf("the refusal names a share of %d of %d, want 1 of 8", capacity.AmbientShare, capacity.Capacity)
	}
	sentence := capacity.Error()
	for _, want := range []string{string(PreviewExtra), "6000 kbps", "15000 kbps"} {
		if !strings.Contains(sentence, want) {
			t.Fatalf("the refusal does not name %q, so an operator cannot see which numbers produced it: %q", want, sentence)
		}
	}
	// The operator's own frame is NOT refused by the grid's budget: the reserve is
	// kept for it, and a deployment whose path is tight is exactly when it matters.
	if _, _, err := engine.StartViewer(context.Background(), "worked", "SERIAL-worked", PurposeOperator, MirrorPreview{}); err != nil {
		t.Fatalf("the operator's own frame was refused by the grid's transport budget: %v", err)
	}
}

// TestTheEngineRefusesABudgetBelowOneStreamsCost is an internally inconsistent
// configuration refused at COMPOSITION: a plane whose stated budget cannot carry a
// single stream at its own preview setting has no live tile to carry, and every
// refusal it would otherwise hand an operator would blame the grid's share for a
// number the deployment chose.
func TestTheEngineRefusesABudgetBelowOneStreamsCost(t *testing.T) {
	_, err := NewMirrorEngine(MirrorEngineConfig{
		Dialer: newFakeDialer(), MaxSessions: 4, OperatorReserve: 1,
		Preview: MirrorPreview{Quality: PreviewExtra}, TransportBudgetKbps: 5000,
	})
	if err == nil {
		t.Fatal("an engine was built with a transport budget that cannot carry one stream at its own preview setting")
	}
	for _, want := range []string{"5000 kbps", string(PreviewExtra), "6000 kbps"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q, so the deployment cannot be corrected from it: %v", want, err)
		}
	}
}

// TestTheEngineRefusesAReserveThatIsTheWholeCapacity is the other internally
// inconsistent pair, refused at composition with the reason named: a reserve that is
// the whole capacity leaves the console's grid no place to carry a picture in.
func TestTheEngineRefusesAReserveThatIsTheWholeCapacity(t *testing.T) {
	for _, reserve := range []int{4, 5} {
		_, err := NewMirrorEngine(MirrorEngineConfig{Dialer: newFakeDialer(), MaxSessions: 4, OperatorReserve: reserve})
		if err == nil {
			t.Fatalf("an engine was built with a reserve of %d against a capacity of 4, which leaves the grid no place", reserve)
		}
		for _, want := range []string{"reserve", "4", "capacity"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal does not name %q, so the deployment cannot be corrected from it: %v", want, err)
			}
		}
	}
}

// TestTheEngineRefusesAPreviewLevelItCannotPrice: a level is what a stream COSTS, so
// a name this plane cannot price is refused where the plane is built rather than
// defaulted - a grid sized against a stream nobody stated is the failure this whole
// bound exists to prevent.
func TestTheEngineRefusesAPreviewLevelItCannotPrice(t *testing.T) {
	_, err := NewMirrorEngine(MirrorEngineConfig{
		Dialer: newFakeDialer(), Preview: MirrorPreview{Quality: MirrorPreviewQuality("4k")},
	})
	if err == nil {
		t.Fatal("an engine was built for a preview level this plane cannot price")
	}
	for _, want := range []string{"4k", "low", "medium", "high", "extra"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestTransportBudgetFromEnvReadsTheStatedBudget: the input is read where the
// deployment states it, and what the deployment stated is what the plane gets.
func TestTransportBudgetFromEnvReadsTheStatedBudget(t *testing.T) {
	budget, err := TransportBudgetFromEnv(envLookup(map[string]string{
		EnvTransportBudgetKbps: " 24000 ",
	}))
	if err != nil {
		t.Fatalf("a stated transport budget was refused: %v", err)
	}
	if budget != 24000 {
		t.Fatalf("the deployment's transport budget reads %d kbps, want 24000", budget)
	}

	// A deployment that states none gets the measured default, which is stated in the
	// startup line rather than being invisible.
	budget, err = TransportBudgetFromEnv(envLookup(map[string]string{}))
	if err != nil {
		t.Fatalf("a deployment that stated no transport budget was refused: %v", err)
	}
	if budget != DefaultTransportBudgetKbps {
		t.Fatalf("an unstated deployment reads a budget of %d, want the measured default (%d)",
			budget, DefaultTransportBudgetKbps)
	}
}

// TestTransportBudgetFromEnvRefusesWhatItCannotRead is the same rule the capacity
// beside it has: a mistyped bound is a startup line, not a grid whose pictures are
// inexplicably late.
func TestTransportBudgetFromEnvRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{name: "a budget that is not a number", value: "plenty"},
		{name: "a budget of zero", value: "0"},
		{name: "a negative budget", value: "-32000"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := TransportBudgetFromEnv(envLookup(map[string]string{EnvTransportBudgetKbps: test.value}))
			if err == nil {
				t.Fatalf("%s was accepted: the plane would budget a grid against a bound nobody stated", test.name)
			}
			for _, want := range []string{EnvTransportBudgetKbps, test.value} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestThePricedLevelsComeFromTheEncodeTable pins the one thing that keeps a budget
// honest: the cost it prices a level at is the level's own bit rate cap, so the
// numbers a grid is sized against are the numbers the device's encoder is held to.
func TestThePricedLevelsComeFromTheEncodeTable(t *testing.T) {
	want := []ProfileBitrate{
		{Quality: PreviewLow, BitrateKbps: 500},
		{Quality: PreviewMedium, BitrateKbps: 1200},
		{Quality: PreviewHigh, BitrateKbps: 2500},
		{Quality: PreviewExtra, BitrateKbps: 6000},
	}
	priced := PreviewBitrates()
	if len(priced) != len(want) {
		t.Fatalf("this plane prices %d preview level(s), want %d: %v", len(priced), len(want), priced)
	}
	for i, level := range want {
		if priced[i] != level {
			t.Fatalf("level %d is priced %v, want %v", i, priced[i], level)
		}
	}
	for _, level := range priced {
		bitrate, known := PreviewBitrateKbps(level.Quality)
		if !known || bitrate != level.BitrateKbps {
			t.Fatalf("the level %q is priced here as %d kbps but this plane reads %d kbps for it", level.Quality, level.BitrateKbps, bitrate)
		}
	}
	// And a name this product does not have is NOT priced by default: a level that
	// resolved to some other level's cost would be a budget for a stream nobody sends.
	if _, known := PreviewBitrateKbps(MirrorPreviewQuality("4k")); known {
		t.Fatal("this plane priced a preview level it does not have")
	}
}

// TestTheArmedLineStatesTheResolvedBound is the card's reporting requirement: the
// line the operator's frame shows states the capacity, the reserve, the preview
// setting and its per-stream cost, every level this plane can price, the transport
// budget, the spend those two imply and the tile count the bounds produce - so a
// deployment can be read back from a frame without guessing which default is in force.
func TestTheArmedLineStatesTheResolvedBound(t *testing.T) {
	engine := newEngine(t, newFakeDialer(), MirrorEngineConfig{
		MaxSessions: 8, OperatorReserve: 1, Preview: MirrorPreview{Quality: PreviewExtra, FrameRate: 15}, TransportBudgetKbps: 32000,
	})
	host := NewMirrorHost(MirrorHostConfig{Engine: engine})

	line := host.State()
	for _, want := range []string{
		"capacity 8 device session(s)",
		"1 kept for the operator's own frame",
		"a stream at extra costs 6000 kbps",
		"transport budget 32000 kbps, this plane's spend 48000 kbps",
		"the console's grid may carry 4 live tile picture(s), bound by transport_budget",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the armed line does not state %q, so the deployment cannot be read back from it:\n%s", want, line)
		}
	}
	// The per-level cost of EVERY level is on the line, not only the selected one:
	// "the grid carries fewer tiles than it used to" has to be answerable from the
	// frame, and that question is about the other qualities too.
	for _, want := range []string{"low=500kbps", "medium=1200kbps", "high=2500kbps", "extra=6000kbps"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the armed line does not state the per-level cost %q:\n%s", want, line)
		}
	}
}
