package migrations_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"drift.local/drift-next/db/migrations"
	migrationrunner "drift.local/drift-next/internal/platform/migrations"
)

// openDatabaseAt opens an existing database file through the same connection
// factory the local service uses.
func openDatabaseAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := migrationrunner.Open(context.Background(), path, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open(%s) error = %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// duplicateFleetRow is one device row of the lab's duplicate shape: the name the
// registry recorded for it, the transport it was registered at, and whether the
// plane held that transport current before the reconciliation ran.
type duplicateFleetRow struct {
	name     string
	host     string
	port     int
	serial   string
	observed string
	current  bool
	history  int
}

const (
	ghostObserved = "2026-09-18T06:57:11.616273Z"
	liveObserved  = "2026-09-18T07:37:31.253167Z"
	olderObserved = "2026-09-16T20:38:44.748381Z"
)

// duplicateFleet reproduces the 45 device rows ARC-160 found on the live plane
// against a fleet of 22 units: twenty units registered twice - the row the
// registry named after the device, once at the address the unit no longer
// answers on and once at the address it does - two of them (ALTA 8, ALTA 18)
// also registered a third time by the watcher sweep that could not read their
// names, one unit the registry could only name after a transport
// (192.168.1.129:5555), and the SM-S908E registered once per transport
// (R5CT42GS94Z over USB and 192.168.1.106:5556 over TCP).
func duplicateFleet() []duplicateFleetRow {
	rows := []duplicateFleetRow{}
	pair := func(name, ghostHost, liveHost string) {
		rows = append(rows,
			duplicateFleetRow{name: name, host: ghostHost, port: 5555, observed: ghostObserved, current: true, history: 10},
			duplicateFleetRow{name: name, host: liveHost, port: 5555, observed: liveObserved, current: true, history: 11},
		)
	}
	pair("ALTA 1", "192.168.1.104", "192.168.1.142")
	pair("ALTA 2", "192.168.1.111", "192.168.1.132")
	pair("ALTA 3", "192.168.1.107", "192.168.1.145")
	pair("ALTA 4", "192.168.1.125", "192.168.1.143")
	pair("ALTA 5", "192.168.1.109", "192.168.1.147")
	pair("ALTA 6", "192.168.1.116", "192.168.1.144")
	pair("ALTA 7", "192.168.1.113", "192.168.1.146")
	pair("ALTA 9", "192.168.1.110", "192.168.1.141")
	pair("ALTA 10", "192.168.1.114", "192.168.1.140")
	pair("ALTA 11", "192.168.1.117", "192.168.1.133")
	pair("ALTA 12", "192.168.1.120", "192.168.1.135")
	pair("ALTA 13", "192.168.1.123", "192.168.1.134")
	pair("ALTA 14", "192.168.1.112", "192.168.1.136")
	pair("ALTA 15", "192.168.1.119", "192.168.1.137")
	pair("ALTA 16", "192.168.1.115", "192.168.1.138")
	pair("ALTA 17", "192.168.1.118", "192.168.1.139")
	pair("ALTA 19", "192.168.1.124", "192.168.1.130")
	pair("ALTA 20", "192.168.1.126", "192.168.1.131")
	rows = append(rows,
		// ALTA 8: the scan row, the watcher row it minted without a name, and
		// the row the unit answers at now.
		duplicateFleetRow{name: "ALTA 8", host: "192.168.1.122", port: 5555, observed: ghostObserved, current: true, history: 10},
		duplicateFleetRow{name: "192.168.1.148:5555", host: "192.168.1.148", port: 5555, observed: ghostObserved, current: true, history: 13},
		duplicateFleetRow{name: "ALTA 8", host: "192.168.1.101", port: 5555, observed: liveObserved, current: true},
		// ALTA 18: the same three, with two scan rows.
		duplicateFleetRow{name: "ALTA 18", host: "192.168.1.127", port: 5555, observed: ghostObserved, current: true, history: 10},
		duplicateFleetRow{name: "192.168.1.149:5555", host: "192.168.1.149", port: 5555, observed: ghostObserved, current: true, history: 13},
		duplicateFleetRow{name: "ALTA 18", host: "192.168.1.128", port: 5555, observed: liveObserved, current: true, history: 1},
		// A unit the registry could only name after its transport.
		duplicateFleetRow{name: "192.168.1.129:5555", host: "192.168.1.129", port: 5555, observed: olderObserved, current: true},
		// The SM-S908E, observed over two transports at once.
		duplicateFleetRow{name: "192.168.1.106:5556", host: "192.168.1.106", port: 5556, observed: "2026-09-17T08:43:53.275361Z", current: true},
		duplicateFleetRow{name: "R5CT42GS94Z", serial: "R5CT42GS94Z", port: 0, observed: "2026-09-17T22:39:54.575478Z", current: true, history: 2},
	)
	return rows
}

func seedDuplicateFleet(t *testing.T, db *sql.DB) {
	t.Helper()
	const workspace = "reconcile-w"
	execSQL(t, db, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES (?, 'Reconcile', 'active', ?, ?)`, workspace, testTime, testTime)
	execSQL(t, db, `INSERT INTO device_groups (id, workspace_id, name, state, created_at, updated_at, row_version) VALUES ('group-1', ?, 'Lab floor', 'active', ?, ?, 1)`, workspace, testTime, testTime)
	for index, row := range duplicateFleet() {
		deviceID := "device-" + strconv.Itoa(index)
		serial := row.serial
		if serial == "" {
			serial = row.host + ":" + strconv.Itoa(row.port)
		}
		endpointType := "adb_tcp"
		var host any = row.host
		if row.serial != "" {
			endpointType = "adb_usb"
			host = nil
		}
		execSQL(t, db, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, last_seen_at, created_at, updated_at, row_version) VALUES (?, ?, ?, 'SM_G9750', 'active', ?, ?, ?, 1)`,
			deviceID, workspace, row.name, row.observed, testTime, testTime)
		// Older, superseded history: every endpoint row must survive the
		// reconciliation unchanged.
		for history := 0; history < row.history; history++ {
			execSQL(t, db, `INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, host, port, state, observed_at, superseded_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'superseded', ?, ?)`,
				deviceID+"-history-"+strconv.Itoa(history), workspace, deviceID, endpointType, serial, host, row.port,
				"2026-09-17T15:13:40.878811Z", ghostObserved)
		}
		state := "observed"
		var supersededAt any
		if row.current {
			state = "current"
		}
		execSQL(t, db, `INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, host, port, state, observed_at, superseded_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			deviceID+"-live", workspace, deviceID, endpointType, serial, host, row.port, state, row.observed, supersededAt)
	}
}

// endpointHistoryFingerprint is what "every endpoint's history preserved" means
// concretely: the id, transport and observation of every endpoint row, with the
// rows that only record the reconciliation's own bookkeeping (state, and when a
// transport stopped being current) left out.
func endpointHistoryFingerprint(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT id || '|' || endpoint_type || '|' || COALESCE(serial, '') || '|' || COALESCE(host, '') || '|' || port || '|' || observed_at FROM device_endpoints`)
	if err != nil {
		t.Fatalf("endpoint fingerprint query error = %v", err)
	}
	defer rows.Close()
	fingerprints := []string{}
	for rows.Next() {
		var fingerprint string
		if err := rows.Scan(&fingerprint); err != nil {
			t.Fatalf("endpoint fingerprint scan error = %v", err)
		}
		fingerprints = append(fingerprints, fingerprint)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("endpoint fingerprint rows error = %v", err)
	}
	sort.Strings(fingerprints)
	return fingerprints
}

func scalarCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count query error = %v\nquery: %s", err, query)
	}
	return count
}

func scalarString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("value query error = %v\nquery: %s", err, query)
	}
	return value
}

// observedAtBySerial is the newest observation the plane recorded for each
// transport before the reconciliation moved any endpoint row.
func observedAtBySerial(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT serial, MAX(observed_at) FROM device_endpoints WHERE serial IS NOT NULL GROUP BY serial`)
	if err != nil {
		t.Fatalf("observed transport query error = %v", err)
	}
	defer rows.Close()
	observed := map[string]string{}
	for rows.Next() {
		var serial, observedAt string
		if err := rows.Scan(&serial, &observedAt); err != nil {
			t.Fatalf("observed transport scan error = %v", err)
		}
		observed[serial] = observedAt
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("observed transport rows error = %v", err)
	}
	return observed
}

// serialsByDevice is the set of transports each surviving device holds after the
// reconciliation: the unit's own rows and every row folded into it.
func serialsByDevice(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	rows, err := db.Query(`SELECT e.device_id, e.serial FROM device_endpoints AS e JOIN devices AS d ON d.id = e.device_id WHERE d.state <> 'retired' AND e.serial IS NOT NULL`)
	if err != nil {
		t.Fatalf("device transport query error = %v", err)
	}
	defer rows.Close()
	serials := map[string][]string{}
	for rows.Next() {
		var deviceID, serial string
		if err := rows.Scan(&deviceID, &serial); err != nil {
			t.Fatalf("device transport scan error = %v", err)
		}
		serials[deviceID] = append(serials[deviceID], serial)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("device transport rows error = %v", err)
	}
	return serials
}

// TestDeviceIdentityReconciliationFoldsDuplicates is the acceptance test for the
// owner's decision: 45 device rows in, 22 real devices out, every endpoint's
// history preserved, one current endpoint per device, and the duplicates retired
// in place with the reason recorded.
func TestDeviceIdentityReconciliationFoldsDuplicates(t *testing.T) {
	db := openUpgradeDB(t, 24)
	seedDuplicateFleet(t, db)

	devicesBefore := scalarCount(t, db, `SELECT COUNT(*) FROM devices`)
	if devicesBefore != 45 {
		t.Fatalf("seeded devices = %d, want the lab's 45 rows", devicesBefore)
	}
	historyBefore := endpointHistoryFingerprint(t, db)
	rowsBefore := scalarCount(t, db, `SELECT COUNT(*) FROM device_endpoints`)
	observedBefore := observedAtBySerial(t, db)
	// The operator's current placement of two units sits on the row the registry
	// wrote it against, plus one ended placement that is history.
	ghostALTA13 := scalarString(t, db, `SELECT device_id FROM device_endpoints WHERE serial = '192.168.1.123:5555'`)
	ghostALTA1 := scalarString(t, db, `SELECT device_id FROM device_endpoints WHERE serial = '192.168.1.104:5555'`)
	execSQL(t, db, `INSERT INTO device_group_memberships (id, workspace_id, group_id, device_id, position, state, started_at, ended_at) VALUES ('placement-active-13', 'reconcile-w', 'group-1', ?, 1, 'active', ?, NULL)`, ghostALTA13, testTime)
	execSQL(t, db, `INSERT INTO device_group_memberships (id, workspace_id, group_id, device_id, position, state, started_at, ended_at) VALUES ('placement-active-1', 'reconcile-w', 'group-1', ?, 2, 'active', ?, NULL)`, ghostALTA1, testTime)
	execSQL(t, db, `INSERT INTO device_group_memberships (id, workspace_id, group_id, device_id, position, state, started_at, ended_at) VALUES ('placement-ended-13', 'reconcile-w', 'group-1', ?, 3, 'ended', ?, ?)`, ghostALTA13, testTime, testTime)
	if devicesWithCurrent := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT device_id FROM device_endpoints WHERE state = 'current' GROUP BY workspace_id, device_id HAVING COUNT(*) = 1)`); devicesWithCurrent != 45 {
		t.Fatalf("seeded devices holding one current endpoint = %d, want one per seeded row", devicesWithCurrent)
	}

	if err := applyLatestMigrations(t, db); err != nil {
		t.Fatalf("Apply(0025) error = %v", err)
	}

	// Nothing was deleted: the duplicate rows are retired in place, because the
	// audit that proves how they were created references them.
	if devicesAfter := scalarCount(t, db, `SELECT COUNT(*) FROM devices`); devicesAfter != devicesBefore {
		t.Fatalf("devices after reconciliation = %d, want the same %d rows (retired, not deleted)", devicesAfter, devicesBefore)
	}
	if retired := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE state = 'retired'`); retired != 23 {
		t.Fatalf("retired devices = %d, want 23", retired)
	}
	if live := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE state <> 'retired'`); live != 22 {
		t.Fatalf("real devices after reconciliation = %d, want 22 (the 20 ALTA units, 192.168.1.129:5555 and the SM-S908E)", live)
	}

	// Every endpoint row survived, with its own observation untouched.
	historyAfter := endpointHistoryFingerprint(t, db)
	if len(historyAfter) != len(historyBefore) {
		t.Fatalf("endpoint rows after reconciliation = %d, want the same %d", len(historyAfter), len(historyBefore))
	}
	for index := range historyBefore {
		if historyAfter[index] != historyBefore[index] {
			t.Fatalf("endpoint history changed at %d:\n before %s\n after  %s", index, historyBefore[index], historyAfter[index])
		}
	}
	if rowsAfter := scalarCount(t, db, `SELECT COUNT(*) FROM device_endpoints`); rowsAfter != rowsBefore {
		t.Fatalf("endpoint rows after reconciliation = %d, want %d", rowsAfter, rowsBefore)
	}
	if stranded := scalarCount(t, db, `SELECT COUNT(*) FROM device_endpoints AS e JOIN devices AS d ON d.id = e.device_id WHERE d.state = 'retired'`); stranded != 0 {
		t.Fatalf("endpoints still on a retired device = %d, want 0", stranded)
	}

	// One current endpoint per device. The pre-fix plane held one current
	// transport per row, so a unit registered twice held two; the reconciliation
	// settles that to the transport the unit was LAST observed at, and never
	// awards a current endpoint to a device that held none.
	if over := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT device_id FROM device_endpoints WHERE state = 'current' GROUP BY workspace_id, device_id HAVING COUNT(*) > 1)`); over != 0 {
		t.Fatalf("devices with more than one current endpoint = %d, want 0", over)
	}
	if devicesWithCurrent := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT device_id FROM device_endpoints WHERE state = 'current' GROUP BY workspace_id, device_id HAVING COUNT(*) = 1)`); devicesWithCurrent != 22 {
		t.Fatalf("devices with exactly one current endpoint = %d, want 22: one per surviving device and none invented", devicesWithCurrent)
	}
	// The surviving current transport is the newest one the unit's rows held
	// before the reconciliation, which is the last transport the unit was
	// reachable at - not some other row's, and not the older lease.
	for deviceID, serials := range serialsByDevice(t, db) {
		current := scalarString(t, db, `SELECT COALESCE(serial, '') FROM device_endpoints WHERE device_id = ? AND state = 'current'`, deviceID)
		expected := ""
		for _, serial := range serials {
			if observedBefore[serial] > observedBefore[expected] {
				expected = serial
			}
		}
		if current != expected {
			t.Fatalf("device %s is current at %q, want %q: the unit's most recently observed transport", deviceID, current, expected)
		}
	}

	// One device, one identity: no display name is held by two live rows, and no
	// endpoint serial is claimed by two live devices.
	if duplicated := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT display_name FROM devices WHERE state <> 'retired' GROUP BY workspace_id, display_name HAVING COUNT(*) > 1)`); duplicated != 0 {
		t.Fatalf("display names held by more than one live device = %d, want 0", duplicated)
	}
	if duplicated := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT e.serial FROM device_endpoints AS e JOIN devices AS d ON d.id = e.device_id WHERE d.state <> 'retired' AND e.serial IS NOT NULL GROUP BY e.workspace_id, e.serial HAVING COUNT(DISTINCT e.device_id) > 1)`); duplicated != 0 {
		t.Fatalf("endpoint serials claimed by more than one live device = %d, want 0", duplicated)
	}

	// The reason is recorded, against both device rows.
	if recorded := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations`); recorded != 23 {
		t.Fatalf("recorded reconciliations = %d, want 23", recorded)
	}
	if unexplained := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations WHERE length(trim(reason)) = 0`); unexplained != 0 {
		t.Fatalf("reconciliations without a reason = %d, want 0", unexplained)
	}
	if mismatched := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations AS r JOIN devices AS d ON d.id = r.duplicate_device_id WHERE d.state <> 'retired'`); mismatched != 0 {
		t.Fatalf("recorded duplicates that are not retired = %d, want 0", mismatched)
	}
	if mismatched := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations AS r JOIN devices AS d ON d.id = r.canonical_device_id WHERE d.state = 'retired'`); mismatched != 0 {
		t.Fatalf("canonical devices that are retired = %d, want 0", mismatched)
	}
	if chained := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations AS r JOIN device_identity_reconciliations AS next ON next.workspace_id = r.workspace_id AND next.duplicate_device_id = r.canonical_device_id`); chained != 0 {
		t.Fatalf("recorded pairings pointing at another duplicate = %d, want 0", chained)
	}
	if violations := foreignKeyViolations(t, db); violations != 0 {
		t.Fatalf("foreign key violations after reconciliation = %d, want 0", violations)
	}

	// The unit's current placement followed the identity; the ended one is
	// history and stayed on the row it happened against.
	for placement, name := range map[string]string{"placement-active-13": "ALTA 13", "placement-active-1": "ALTA 1"} {
		holder := scalarString(t, db, `SELECT d.display_name FROM device_group_memberships AS m JOIN devices AS d ON d.id = m.device_id WHERE m.id = ? AND d.state <> 'retired'`, placement)
		if holder != name {
			t.Fatalf("%s is held by %q, want the live %s identity", placement, holder, name)
		}
	}
	if holder := scalarString(t, db, `SELECT d.state FROM device_group_memberships AS m JOIN devices AS d ON d.id = m.device_id WHERE m.id = 'placement-ended-13'`); holder != "retired" {
		t.Fatalf("the ended placement moved off the row it was made against (holder state %q)", holder)
	}
	if over := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT device_id FROM device_group_memberships WHERE state = 'active' GROUP BY workspace_id, device_id HAVING COUNT(*) > 1)`); over != 0 {
		t.Fatalf("devices holding more than one active placement = %d, want 0", over)
	}

	// The unit's history now sits at the identity the unit answers to: ALTA 13's
	// ghost row held the endpoint at 192.168.1.123, which is now on the row the
	// unit is registered at, and the current transport is the one last observed.
	for _, expectation := range []struct {
		name          string
		currentSerial string
		foldedSerial  string
	}{
		{name: "ALTA 1", currentSerial: "192.168.1.142:5555", foldedSerial: "192.168.1.104:5555"},
		{name: "ALTA 13", currentSerial: "192.168.1.134:5555", foldedSerial: "192.168.1.123:5555"},
		{name: "ALTA 8", currentSerial: "192.168.1.101:5555", foldedSerial: "192.168.1.148:5555"},
		{name: "ALTA 18", currentSerial: "192.168.1.128:5555", foldedSerial: "192.168.1.149:5555"},
		{name: "R5CT42GS94Z", currentSerial: "R5CT42GS94Z", foldedSerial: "192.168.1.106:5556"},
	} {
		live := scalarCount(t, db, `SELECT COUNT(*) FROM device_endpoints AS e JOIN devices AS d ON d.id = e.device_id WHERE d.display_name = ? AND d.state <> 'retired' AND e.state = 'current' AND e.serial = ?`, expectation.name, expectation.currentSerial)
		if live != 1 {
			t.Fatalf("%s: not registered current at %s", expectation.name, expectation.currentSerial)
		}
		folded := scalarCount(t, db, `SELECT COUNT(*) FROM device_endpoints AS e JOIN devices AS d ON d.id = e.device_id WHERE d.display_name = ? AND d.state <> 'retired' AND e.serial = ?`, expectation.name, expectation.foldedSerial)
		if folded == 0 {
			t.Fatalf("%s: the endpoint history at %s did not follow the unit", expectation.name, expectation.foldedSerial)
		}
	}

	// A transport-named row no evidence pairs with a unit is a unit of its own,
	// not a duplicate: over-merging would move one unit's history onto another's
	// identity, which is the defect this repairs.
	if strays := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE display_name = '192.168.1.129:5555' AND state <> 'retired'`); strays != 1 {
		t.Fatalf("the unpaired transport-named row was reconciled, want it left alone")
	}
	if kept := scalarCount(t, db, `SELECT COUNT(*) FROM device_endpoints AS e JOIN devices AS d ON d.id = e.device_id WHERE d.display_name = '192.168.1.129:5555' AND e.serial = '192.168.1.129:5555'`); kept != 1 {
		t.Fatalf("the unpaired unit's endpoint history = %d, want 1", kept)
	}

	// Idempotent: running the same file again changes nothing.
	statement, err := fs.ReadFile(migrations.SQLiteFiles, "0025_device_identity_reconciliation.sql")
	if err != nil {
		t.Fatalf("ReadFile(0025) error = %v", err)
	}
	rowVersionsBefore := scalarCount(t, db, `SELECT COALESCE(SUM(row_version), 0) FROM devices`)
	updatedBefore := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE state = 'retired'`)
	if _, err := db.Exec(string(statement)); err != nil {
		t.Fatalf("re-applying 0025 error = %v", err)
	}
	if recorded := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations`); recorded != 23 {
		t.Fatalf("reconciliations after a second apply = %d, want 23 unchanged", recorded)
	}
	if retired := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE state = 'retired'`); retired != updatedBefore {
		t.Fatalf("retired devices after a second apply = %d, want %d", retired, updatedBefore)
	}
	if rowVersionsAfter := scalarCount(t, db, `SELECT COALESCE(SUM(row_version), 0) FROM devices`); rowVersionsAfter != rowVersionsBefore {
		t.Fatalf("row versions after a second apply = %d, want %d: the migration must not touch a row twice", rowVersionsAfter, rowVersionsBefore)
	}
	historySecond := endpointHistoryFingerprint(t, db)
	if strings.Join(historySecond, "\n") != strings.Join(historyAfter, "\n") {
		t.Fatalf("endpoint history changed on a second apply")
	}
	if current := scalarCount(t, db, `SELECT COUNT(*) FROM device_endpoints WHERE state = 'current'`); current != 22 {
		t.Fatalf("current endpoints after a second apply = %d, want 22", current)
	}
}

// TestDeviceIdentityReconciliationFollowsAChainBetweenTheTwoRules covers the
// shape where the two rules disagree: the unit's older row is the most recently
// observed one, so the name grouping would keep it while the transport pairing
// names the row the unit answers at now. The pairing is then a chain - the
// unnamed row folds onto a row that is itself folded - and the reconciliation
// must follow it to the row that actually survives rather than re-pointing one
// unit's history at a row that is itself a duplicate.
func TestDeviceIdentityReconciliationFollowsAChainBetweenTheTwoRules(t *testing.T) {
	db := openUpgradeDB(t, 24)
	seedDuplicateFleet(t, db)
	execSQL(t, db, `UPDATE device_endpoints SET observed_at = '2026-09-18T09:00:00.000000Z' WHERE serial = '192.168.1.122:5555'`)

	if err := applyLatestMigrations(t, db); err != nil {
		t.Fatalf("Apply(0025) with a chain between the two rules error = %v", err)
	}
	if live := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE state <> 'retired'`); live != 22 {
		t.Fatalf("real devices = %d, want 22", live)
	}
	if chained := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations AS r JOIN device_identity_reconciliations AS next ON next.workspace_id = r.workspace_id AND next.duplicate_device_id = r.canonical_device_id`); chained != 0 {
		t.Fatalf("recorded pairings pointing at another duplicate = %d, want 0", chained)
	}
	// ALTA 8's own rows and the unnamed row the sweep minted for it are one
	// device again.
	holders := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT d.id FROM devices AS d JOIN device_endpoints AS e ON e.device_id = d.id WHERE d.state <> 'retired' AND e.serial IN ('192.168.1.122:5555', '192.168.1.101:5555', '192.168.1.148:5555') GROUP BY d.id)`)
	if holders != 1 {
		t.Fatalf("devices holding ALTA 8's transports = %d, want 1", holders)
	}
	if over := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT device_id FROM device_endpoints WHERE state = 'current' GROUP BY workspace_id, device_id HAVING COUNT(*) > 1)`); over != 0 {
		t.Fatalf("devices with more than one current endpoint = %d, want 0", over)
	}
	if recorded := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations`); recorded != 23 {
		t.Fatalf("recorded reconciliations = %d, want 23", recorded)
	}
}

// TestDeviceIdentityReconciliationMovesOnePlacementPerUnit covers two duplicate
// rows of one unit that each hold a current placement. A device may hold only one
// active membership, so the reconciliation must move one of them and leave the
// other on its retired row - never violate the invariant, and never fail the
// apply on the owner's database.
func TestDeviceIdentityReconciliationMovesOnePlacementPerUnit(t *testing.T) {
	db := openUpgradeDB(t, 24)
	seedDuplicateFleet(t, db)
	unnamedALTA8 := scalarString(t, db, `SELECT device_id FROM device_endpoints WHERE serial = '192.168.1.148:5555'`)
	ghostALTA8 := scalarString(t, db, `SELECT device_id FROM device_endpoints WHERE serial = '192.168.1.122:5555'`)
	execSQL(t, db, `INSERT INTO device_group_memberships (id, workspace_id, group_id, device_id, position, state, started_at, ended_at) VALUES ('placement-a', 'reconcile-w', 'group-1', ?, 1, 'active', ?, NULL)`, ghostALTA8, testTime)
	execSQL(t, db, `INSERT INTO device_group_memberships (id, workspace_id, group_id, device_id, position, state, started_at, ended_at) VALUES ('placement-b', 'reconcile-w', 'group-1', ?, 2, 'active', ?, NULL)`, unnamedALTA8, testTime)

	if err := applyLatestMigrations(t, db); err != nil {
		t.Fatalf("Apply(0025) with two current placements on one unit error = %v", err)
	}
	if live := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE state <> 'retired'`); live != 22 {
		t.Fatalf("real devices = %d, want 22", live)
	}
	if placed := scalarCount(t, db, `SELECT COUNT(*) FROM device_group_memberships AS m JOIN devices AS d ON d.id = m.device_id WHERE m.state = 'active' AND d.state <> 'retired'`); placed != 1 {
		t.Fatalf("current placements on live devices = %d, want 1", placed)
	}
	if over := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT device_id FROM device_group_memberships WHERE state = 'active' GROUP BY workspace_id, device_id HAVING COUNT(*) > 1)`); over != 0 {
		t.Fatalf("devices holding more than one active placement = %d, want 0", over)
	}
	if lost := scalarCount(t, db, `SELECT COUNT(*) FROM device_group_memberships WHERE id IN ('placement-a', 'placement-b')`); lost != 2 {
		t.Fatalf("placements after reconciliation = %d, want 2: nothing is deleted", lost)
	}
}

// TestDeviceIdentityReconciliationLeavesAFreshInstallAlone proves the repair is
// inert where there is nothing to repair: a database with no device rows must
// apply it and come out with an empty reconciliation record, not an error.
func TestDeviceIdentityReconciliationLeavesAFreshInstallAlone(t *testing.T) {
	db := migratedDB(t)
	if recorded := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations`); recorded != 0 {
		t.Fatalf("reconciliations on a fresh install = %d, want 0", recorded)
	}
}

// TestDeviceIdentityReconciliationAgainstLiveCopy runs the same acceptance
// numbers against a copy of the owner's plane when one is supplied, because the
// shape that matters is the live one. Set DRIFT_LIVE_DB_COPY to the path of a
// copy of control-plane.db; the copy is modified in place, so never point it at
// the live file.
func TestDeviceIdentityReconciliationAgainstLiveCopy(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("DRIFT_LIVE_DB_COPY"))
	if path == "" {
		t.Skip("DRIFT_LIVE_DB_COPY is not set: run against a copy of the live database to check the acceptance numbers")
	}
	db := openDatabaseAt(t, path)
	if err := applyLatestMigrations(t, db); err != nil {
		t.Fatalf("Apply(latest) on the live copy error = %v", err)
	}
	devices := scalarCount(t, db, `SELECT COUNT(*) FROM devices`)
	live := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE state <> 'retired'`)
	retired := scalarCount(t, db, `SELECT COUNT(*) FROM devices WHERE state = 'retired'`)
	t.Logf("live copy: %d device rows, %d live, %d retired", devices, live, retired)
	if live != 22 {
		t.Fatalf("live copy real devices = %d, want 22", live)
	}
	if stranded := scalarCount(t, db, `SELECT COUNT(*) FROM device_endpoints AS e JOIN devices AS d ON d.id = e.device_id WHERE d.state = 'retired'`); stranded != 0 {
		t.Fatalf("live copy endpoints left on a retired device = %d, want 0", stranded)
	}
	if over := scalarCount(t, db, `SELECT COUNT(*) FROM (SELECT device_id FROM device_endpoints WHERE state = 'current' GROUP BY workspace_id, device_id HAVING COUNT(*) > 1)`); over != 0 {
		t.Fatalf("live copy devices with more than one current endpoint = %d, want 0", over)
	}
	if unexplained := scalarCount(t, db, `SELECT COUNT(*) FROM device_identity_reconciliations WHERE length(trim(reason)) = 0`); unexplained != 0 {
		t.Fatalf("live copy reconciliations without a reason = %d, want 0", unexplained)
	}
	// The operator's current placements followed the units; nothing live is left
	// pointing at a retired identity, and no ended placement moved.
	if stranded := scalarCount(t, db, `SELECT COUNT(*) FROM device_group_memberships AS m JOIN devices AS d ON d.id = m.device_id WHERE m.state = 'active' AND d.state = 'retired'`); stranded != 0 {
		t.Fatalf("live copy current placements left on a retired identity = %d, want 0", stranded)
	}
	if placed := scalarCount(t, db, `SELECT COUNT(*) FROM device_group_memberships AS m JOIN devices AS d ON d.id = m.device_id WHERE m.state = 'active' AND d.state <> 'retired'`); placed != 3 {
		t.Fatalf("live copy current placements on live devices = %d, want the 3 the fleet had", placed)
	}
	if moved := scalarCount(t, db, `SELECT COUNT(*) FROM device_group_memberships AS m JOIN devices AS d ON d.id = m.device_id WHERE m.state = 'ended' AND d.state <> 'retired'`); moved != 0 {
		t.Fatalf("live copy ended placements that moved = %d, want 0: history stays where it happened", moved)
	}
}
