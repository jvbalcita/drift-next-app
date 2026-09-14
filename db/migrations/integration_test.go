package migrations_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"drift.local/drift-next/db/migrations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	migrationrunner "drift.local/drift-next/internal/platform/migrations"
)

const testTime = "2026-09-14T00:00:00Z"

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "drift.db")
	db, err := migrationrunner.Open(context.Background(), path, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	runner, err := migrationrunner.NewRunner(db, migrations.SQLiteFiles, migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if err := runner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	return db
}

func execSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("Exec() error = %v\nquery: %s", err, query)
	}
}

func expectExecError(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("Exec() error = nil, want constraint or schema error\nquery: %s", query)
	}
}

func migrationFilesThrough(t *testing.T, maxVersion int) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.SQLiteFiles, ".")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		version, err := strconv.Atoi(entry.Name()[:4])
		if err != nil || version > maxVersion {
			continue
		}
		data, err := fs.ReadFile(migrations.SQLiteFiles, entry.Name())
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", entry.Name(), err)
		}
		files[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return files
}

func TestSQLiteMigrationsApplyFresh(t *testing.T) {
	db := migratedDB(t)

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM drift_schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("ledger count error = %v", err)
	}
	if count != 14 {
		t.Fatalf("ledger count = %d, want 14 SQLite migrations", count)
	}

	var foreignKeys string
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys pragma error = %v", err)
	}
	if foreignKeys != "1" {
		t.Fatalf("foreign_keys = %q, want 1", foreignKeys)
	}

	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode pragma error = %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	for _, table := range []string{
		"workspaces", "principals", "operators", "edge_agents", "devices", "device_endpoints",
		"network_profiles", "scan_runs", "scan_candidates", "device_groups", "device_group_memberships",
		"automation_agents", "automation_agent_device_assignments", "observation_snapshots", "device_inventory", "inventory_snapshots", "health_samples", "device_health_current", "device_events",
		"control_sessions", "device_leases", "mirror_sessions", "mirror_targets", "workflows", "workflow_versions",
		"workflow_steps", "runs", "target_set_snapshots", "run_targets", "target_run_steps", "action_attempts", "run_events", "run_ai_candidates", "replay_evidence",
		"accounts", "settings", "policies", "artifacts", "recording_sessions", "recording_event_evidence",
		"skills", "audit_events", "idempotency_keys", "outbox_messages",
	} {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&exists); err != nil {
			t.Fatalf("table %q lookup error = %v", table, err)
		}
		if exists != 1 {
			t.Fatalf("table %q is missing", table)
		}
	}
}

func TestWorkspaceOwnedTablesCarryCanonicalWorkspaceScope(t *testing.T) {
	db := migratedDB(t)
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT IN ('drift_schema_migrations', 'workspaces') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("sqlite_master query error = %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatalf("sqlite_master scan error = %v", err)
		}
		seen++
		if !strings.Contains(strings.ToLower(definition), "workspace_id") {
			t.Errorf("workspace-owned table %q has no workspace_id column", name)
		}
		if strings.Contains(strings.ToLower(definition), "organization_id") {
			t.Errorf("table %q still uses organization_id instead of canonical workspace_id", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("sqlite_master rows error = %v", err)
	}
	if seen < 30 {
		t.Fatalf("workspace-owned table count = %d, want normalized domain tables", seen)
	}
}

func TestSQLiteMigrationsApplyIncrementally(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incremental.db")
	db, err := migrationrunner.Open(context.Background(), path, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for version := 2; version <= 15; version++ {
		runner, err := migrationrunner.NewRunner(db, migrationFilesThrough(t, version), migrationrunner.Options{})
		if err != nil {
			t.Fatalf("NewRunner(%d) error = %v", version, err)
		}
		if err := runner.Apply(context.Background()); err != nil {
			t.Fatalf("Apply(%d) error = %v", version, err)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM drift_schema_migrations WHERE version <= ? AND dirty = 0`, version).Scan(&count); err != nil {
			t.Fatalf("ledger count after %d error = %v", version, err)
		}
		if count != version-1 {
			t.Fatalf("applied migration count after %d = %d, want %d", version, count, version-1)
		}
	}
}

func TestObservationFreshnessMigrationBackfillsLegacyProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-observation.db")
	db, err := migrationrunner.Open(context.Background(), path, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	legacyRunner, err := migrationrunner.NewRunner(db, migrationFilesThrough(t, 6), migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner(6) error = %v", err)
	}
	if err := legacyRunner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply(6) error = %v", err)
	}
	execSQL(t, db, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES ('legacy-w', 'Legacy', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, created_at, updated_at) VALUES ('legacy-d', 'legacy-w', 'Legacy Device', 'fake', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO observation_snapshots (id, workspace_id, device_id, captured_at, capture_correlation_id, coordinate_space, package_name, activity_name, orientation, display_width, display_height, source, protocol_version, capture_status, state, created_at) VALUES ('legacy-o', 'legacy-w', 'legacy-d', ?, 'capture-legacy', 'screen_px', 'com.example.legacy', '.MainActivity', 'portrait', 1080, 1920, 'fake', 'fake-legacy', 'complete', 'recorded', ?)`, testTime, testTime)
	fullRunner, err := migrationrunner.NewRunner(db, migrations.SQLiteFiles, migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner(full) error = %v", err)
	}
	if err := fullRunner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply(full) error = %v", err)
	}
	var modelVersion, freshness string
	var skew int
	if err := db.QueryRow(`SELECT model_version, freshness_token, capture_skew_ms FROM observation_snapshots WHERE id='legacy-o'`).Scan(&modelVersion, &freshness, &skew); err != nil {
		t.Fatalf("backfilled observation lookup error = %v", err)
	}
	if modelVersion != "fake-legacy" || freshness != "legacy:legacy-o" || skew != 0 {
		t.Fatalf("backfilled observation provenance = (%q, %q, %d), want legacy values", modelVersion, freshness, skew)
	}
}

func TestHistoricalPostgresBootstrapIsNotSQLiteInput(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "historical.db")
	db, err := migrationrunner.Open(context.Background(), dbPath, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	source := fstest.MapFS{
		"0001_initial.sql": &fstest.MapFile{Data: []byte("CREATE EXTENSION IF NOT EXISTS pgcrypto;")},
	}
	runner, err := migrationrunner.NewRunner(db, source, migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if err := runner.Apply(context.Background()); platformerrors.CodeOf(err) != platformerrors.CodeInvalidInput {
		t.Fatalf("Apply() error = %v, want invalid_input", err)
	}
}

func TestWorkspaceIsolationAndActiveCardinality(t *testing.T) {
	db := migratedDB(t)
	execSQL(t, db, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES ('w1', 'One', 'active', ?, ?), ('w2', 'Two', 'active', ?, ?)`, testTime, testTime, testTime, testTime)
	execSQL(t, db, `INSERT INTO edge_agents (id, workspace_id, display_name, version, state, created_at, updated_at) VALUES ('e1', 'w1', 'Edge One', 'test', 'active', ?, ?), ('e2', 'w1', 'Edge Two', 'test', 'active', ?, ?)`, testTime, testTime, testTime, testTime)
	execSQL(t, db, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, created_at, updated_at) VALUES ('d1', 'w1', 'Device One', 'fake', 'active', ?, ?), ('d2', 'w1', 'Device Two', 'fake', 'active', ?, ?), ('d3', 'w2', 'Other Workspace Device', 'fake', 'active', ?, ?)`, testTime, testTime, testTime, testTime, testTime, testTime)

	expectExecError(t, db, `INSERT INTO device_bindings (id, workspace_id, device_id, edge_agent_id, state, bound_at) VALUES ('b-cross', 'w1', 'd3', 'e1', 'active', ?)`, testTime)
	execSQL(t, db, `INSERT INTO device_bindings (id, workspace_id, device_id, edge_agent_id, state, bound_at) VALUES ('b1', 'w1', 'd1', 'e1', 'active', ?)`, testTime)
	expectExecError(t, db, `INSERT INTO device_bindings (id, workspace_id, device_id, edge_agent_id, state, bound_at) VALUES ('b2', 'w1', 'd1', 'e2', 'active', ?)`, testTime)

	execSQL(t, db, `INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, state, observed_at) VALUES ('ep1', 'w1', 'd1', 'mock', 'mock-1', 'current', ?)`, testTime)
	expectExecError(t, db, `INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, state, observed_at) VALUES ('ep2', 'w1', 'd1', 'mock', 'mock-2', 'current', ?)`, testTime)

	execSQL(t, db, `INSERT INTO device_groups (id, workspace_id, name, state, created_at, updated_at) VALUES ('g1', 'w1', 'Group One', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO device_group_memberships (id, workspace_id, group_id, device_id, position, state, started_at) VALUES ('gm1', 'w1', 'g1', 'd1', 0, 'active', ?)`, testTime)
	expectExecError(t, db, `INSERT INTO device_group_memberships (id, workspace_id, group_id, device_id, position, state, started_at) VALUES ('gm2', 'w1', 'g1', 'd1', 1, 'active', ?)`, testTime)

	execSQL(t, db, `INSERT INTO automation_agents (id, workspace_id, name, state, created_at, updated_at) VALUES ('aa1', 'w1', 'Agent One', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO automation_agent_profiles (id, workspace_id, automation_agent_id, version, state, memory_scope, memory_retention_class, created_at) VALUES ('p1', 'w1', 'aa1', 1, 'published', 'agent', 'current', ?)`, testTime)
	execSQL(t, db, `INSERT INTO automation_agent_device_assignments (id, workspace_id, automation_agent_id, profile_id, device_id, state, assigned_at) VALUES ('as1', 'w1', 'aa1', 'p1', 'd1', 'active', ?)`, testTime)
	execSQL(t, db, `INSERT INTO automation_agent_device_assignments (id, workspace_id, automation_agent_id, profile_id, device_id, state, assigned_at) VALUES ('as2', 'w1', 'aa1', 'p1', 'd2', 'active', ?)`, testTime)
	expectExecError(t, db, `INSERT INTO automation_agent_device_assignments (id, workspace_id, automation_agent_id, profile_id, device_id, state, assigned_at) VALUES ('as3', 'w1', 'aa1', 'p1', 'd1', 'active', ?)`, testTime)

	execSQL(t, db, `INSERT INTO network_profiles (id, workspace_id, name, address_policy, ports_json, is_default, state, created_at, updated_at) VALUES ('np1', 'w1', 'Default', '127.0.0.1/32', '[5555]', 1, 'active', ?, ?)`, testTime, testTime)
	expectExecError(t, db, `INSERT INTO network_profiles (id, workspace_id, name, address_policy, ports_json, is_default, state, created_at, updated_at) VALUES ('np2', 'w1', 'Second', '127.0.0.2/32', '[5555]', 1, 'active', ?, ?)`, testTime, testTime)

	execSQL(t, db, `INSERT INTO control_sessions (id, workspace_id, holder_id, state, created_at, expires_at) VALUES ('cs1', 'w1', 'operator-1', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO device_leases (id, workspace_id, device_id, session_id, holder_id, fencing_token, state, acquired_at, expires_at) VALUES ('l1', 'w1', 'd1', 'cs1', 'operator-1', 1, 'active', ?, ?), ('l2', 'w1', 'd2', 'cs1', 'operator-1', 1, 'active', ?, ?)`, testTime, testTime, testTime, testTime)
	expectExecError(t, db, `INSERT INTO device_leases (id, workspace_id, device_id, session_id, holder_id, fencing_token, state, acquired_at, expires_at) VALUES ('l3', 'w1', 'd1', 'cs1', 'operator-2', 2, 'active', ?, ?)`, testTime, testTime)
}

func TestActiveLeaseUniquenessUnderConcurrentWriters(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "concurrent.db")
	dbOne, err := migrationrunner.Open(context.Background(), dbPath, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	t.Cleanup(func() { _ = dbOne.Close() })
	dbTwo, err := migrationrunner.Open(context.Background(), dbPath, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	t.Cleanup(func() { _ = dbTwo.Close() })
	dbOne.SetMaxOpenConns(1)
	dbTwo.SetMaxOpenConns(1)
	runner, err := migrationrunner.NewRunner(dbOne, migrations.SQLiteFiles, migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if err := runner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	execSQL(t, dbOne, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES ('w1', 'One', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, dbOne, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, created_at, updated_at) VALUES ('d1', 'w1', 'Device One', 'fake', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, dbOne, `INSERT INTO control_sessions (id, workspace_id, holder_id, state, created_at, expires_at) VALUES ('cs1', 'w1', 'operator-1', 'active', ?, ?)`, testTime, testTime)

	start := make(chan struct{})
	results := make(chan error, 2)
	insert := func(db *sql.DB, leaseID string) {
		<-start
		_, insertErr := db.Exec(`INSERT INTO device_leases (id, workspace_id, device_id, session_id, holder_id, fencing_token, state, acquired_at, expires_at) VALUES (?, 'w1', 'd1', 'cs1', ?, ?, 'active', ?, ?)`, leaseID, leaseID, leaseID[1:], testTime, testTime)
		results <- insertErr
	}
	go insert(dbOne, "l1")
	go insert(dbTwo, "l2")
	close(start)

	successes := 0
	failures := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent active lease inserts succeeded=%d failed=%d, want one of each", successes, failures)
	}
}

func TestMirrorTargetsHaveIndependentLeasesAndResults(t *testing.T) {
	db := migratedDB(t)
	execSQL(t, db, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES ('w1', 'One', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, created_at, updated_at) VALUES ('source', 'w1', 'Source', 'fake', 'active', ?, ?), ('follower-1', 'w1', 'Follower One', 'fake', 'active', ?, ?), ('follower-2', 'w1', 'Follower Two', 'fake', 'active', ?, ?)`, testTime, testTime, testTime, testTime, testTime, testTime)
	execSQL(t, db, `INSERT INTO control_sessions (id, workspace_id, holder_id, state, created_at, expires_at) VALUES ('cs1', 'w1', 'operator-1', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO device_leases (id, workspace_id, device_id, session_id, holder_id, fencing_token, state, acquired_at, expires_at) VALUES ('lf1', 'w1', 'follower-1', 'cs1', 'operator-1', 1, 'active', ?, ?), ('lf2', 'w1', 'follower-2', 'cs1', 'operator-1', 1, 'active', ?, ?)`, testTime, testTime, testTime, testTime)
	execSQL(t, db, `INSERT INTO mirror_sessions (id, workspace_id, control_session_id, source_device_id, state, failure_policy, created_at) VALUES ('ms1', 'w1', 'cs1', 'source', 'active', 'continue', ?)`, testTime)
	execSQL(t, db, `INSERT INTO mirror_targets (id, workspace_id, mirror_session_id, follower_device_id, lease_id, state, created_at) VALUES ('mt1', 'w1', 'ms1', 'follower-1', 'lf1', 'leased', ?), ('mt2', 'w1', 'ms1', 'follower-2', 'lf2', 'leased', ?)`, testTime, testTime)
	var targetCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mirror_targets WHERE workspace_id = 'w1' AND mirror_session_id = 'ms1'`).Scan(&targetCount); err != nil {
		t.Fatalf("mirror target count error = %v", err)
	}
	if targetCount != 2 {
		t.Fatalf("mirror target count = %d, want 2", targetCount)
	}
	expectExecError(t, db, `INSERT INTO mirror_targets (id, workspace_id, mirror_session_id, follower_device_id, lease_id, state, created_at) VALUES ('mt3', 'w1', 'ms1', 'source', 'lf1', 'leased', ?)`, testTime)
}

func TestRetirementPreservesReferencedHistory(t *testing.T) {
	db := migratedDB(t)
	execSQL(t, db, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES ('w1', 'One', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO devices (id, workspace_id, display_name, platform_version, state, created_at, updated_at) VALUES ('d1', 'w1', 'Device One', 'fake', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO device_endpoints (id, workspace_id, device_id, endpoint_type, serial, state, observed_at) VALUES ('ep1', 'w1', 'd1', 'mock', 'mock-1', 'current', ?)`, testTime)
	execSQL(t, db, `UPDATE devices SET state = 'retired', updated_at = ?, row_version = row_version + 1 WHERE workspace_id = 'w1' AND id = 'd1'`, testTime)
	var state string
	if err := db.QueryRow(`SELECT state FROM devices WHERE workspace_id = 'w1' AND id = 'd1'`).Scan(&state); err != nil {
		t.Fatalf("retired device lookup error = %v", err)
	}
	if state != "retired" {
		t.Fatalf("device state = %q, want retired", state)
	}
	expectExecError(t, db, `DELETE FROM devices WHERE workspace_id = 'w1' AND id = 'd1'`)
	var endpointCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_endpoints WHERE id = 'ep1'`).Scan(&endpointCount); err != nil {
		t.Fatalf("endpoint history lookup error = %v", err)
	}
	if endpointCount != 1 {
		t.Fatal("referenced endpoint history was removed")
	}
}

func TestIdempotencyAndOutboxAtomicity(t *testing.T) {
	db := migratedDB(t)
	execSQL(t, db, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES ('w1', 'One', 'active', ?, ?)`, testTime, testTime)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO settings (id, workspace_id, scope, setting_key, value_json, state, created_at, updated_at) VALUES ('set-rollback', 'w1', 'workspace', 'mode', '{}', 'active', ?, ?)`, testTime, testTime); err != nil {
		t.Fatalf("insert rollback setting error = %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO outbox_messages (id, workspace_id, event_name, schema_version, correlation_id, payload_json, state, created_at) VALUES ('out-rollback', 'w1', 'setting.changed', 1, 'corr-1', '{}', 'recorded', ?)`, testTime); err != nil {
		t.Fatalf("insert rollback outbox error = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE id = 'set-rollback'`).Scan(&count); err != nil {
		t.Fatalf("rollback setting count error = %v", err)
	}
	if count != 0 {
		t.Fatal("rolled-back setting was retained")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_messages WHERE id = 'out-rollback'`).Scan(&count); err != nil {
		t.Fatalf("rollback outbox count error = %v", err)
	}
	if count != 0 {
		t.Fatal("rolled-back outbox message was retained")
	}

	execSQL(t, db, `INSERT INTO idempotency_keys (id, workspace_id, key, operation_type, operation_id, request_hash, outcome, created_at) VALUES ('idem-1', 'w1', 'request-1', 'setting.update', 'set-1', 'hash-1', 'reserved', ?)`, testTime)
	expectExecError(t, db, `INSERT INTO idempotency_keys (id, workspace_id, key, operation_type, operation_id, request_hash, outcome, created_at) VALUES ('idem-2', 'w1', 'request-1', 'setting.update', 'set-2', 'hash-2', 'reserved', ?)`, testTime)
}

func TestSensitiveFixtureCannotEnterOrdinaryRows(t *testing.T) {
	db := migratedDB(t)
	execSQL(t, db, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES ('w1', 'One', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO settings (id, workspace_id, scope, setting_key, value_json, state, created_at, updated_at) VALUES ('safe', 'w1', 'workspace', 'mode', '{}', 'active', ?, ?)`, testTime, testTime)
	expectExecError(t, db, `INSERT INTO settings (id, workspace_id, scope, setting_key, value_json, state, created_at, updated_at) VALUES ('secret', 'w1', 'workspace', 'mode', '{"token":"[REDACTED]"}', 'active', ?, ?)`, testTime, testTime)
	execSQL(t, db, `INSERT INTO account_sources (id, workspace_id, provider, display_name, state, created_at, updated_at) VALUES ('source-1', 'w1', 'fixture', 'Fixture Source', 'active', ?, ?)`, testTime, testTime)
	expectExecError(t, db, `INSERT INTO accounts (id, workspace_id, source_id, external_ref, label, state, created_at, updated_at, credentials) VALUES ('account-1', 'w1', 'source-1', 'ref-1', 'Label', 'active', ?, ?, '[REDACTED]')`, testTime, testTime)
	execSQL(t, db, `INSERT INTO recording_sessions (id, workspace_id, state, source, started_at, created_at) VALUES ('recording-1', 'w1', 'recording', 'fake', ?, ?)`, testTime, testTime)
	expectExecError(t, db, `INSERT INTO recording_events (id, workspace_id, recording_session_id, sequence, logical_action, redaction_state, sensitive, created_at) VALUES ('recording-event-raw', 'w1', 'recording-1', 0, 'tap', 'required', 1, ?)`, testTime)
	execSQL(t, db, `INSERT INTO recording_events (id, workspace_id, recording_session_id, sequence, logical_action, redaction_state, sensitive, created_at) VALUES ('recording-event-redacted', 'w1', 'recording-1', 1, 'tap', 'redacted', 1, ?)`, testTime)
}

func TestSQLiteDatabaseCanBeRestored(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.db")
	restoredPath := filepath.Join(root, "restored.db")
	db, err := migrationrunner.Open(context.Background(), sourcePath, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open(source) error = %v", err)
	}
	runner, err := migrationrunner.NewRunner(db, migrations.SQLiteFiles, migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner(source) error = %v", err)
	}
	if err := runner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply(source) error = %v", err)
	}
	execSQL(t, db, `INSERT INTO workspaces (id, name, state, created_at, updated_at) VALUES ('restore-w1', 'Restore', 'active', ?, ?)`, testTime, testTime)
	if _, err := db.Exec(`PRAGMA wal_checkpoint(FULL)`); err != nil {
		t.Fatalf("wal checkpoint error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close(source) error = %v", err)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile(source) error = %v", err)
	}
	if err := os.WriteFile(restoredPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(restore) error = %v", err)
	}
	restored, err := migrationrunner.Open(context.Background(), restoredPath, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open(restore) error = %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	restoredRunner, err := migrationrunner.NewRunner(restored, migrations.SQLiteFiles, migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner(restore) error = %v", err)
	}
	if err := restoredRunner.Apply(context.Background()); err != nil {
		t.Fatalf("Apply(restore) error = %v", err)
	}
	var name string
	if err := restored.QueryRow(`SELECT name FROM workspaces WHERE id = 'restore-w1'`).Scan(&name); err != nil {
		t.Fatalf("restored workspace lookup error = %v", err)
	}
	if name != "Restore" {
		t.Fatalf("restored workspace name = %q, want Restore", name)
	}
}

func TestMigrationDirtyStateStillBlocksDomainApply(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dirty.db")
	db, err := migrationrunner.Open(context.Background(), dbPath, migrationrunner.OpenOptions{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	bad := fstest.MapFS{
		"0002_broken.sql": &fstest.MapFile{Data: []byte("CREATE TABLE broken (;")},
	}
	runner, err := migrationrunner.NewRunner(db, bad, migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner(bad) error = %v", err)
	}
	if err := runner.Apply(context.Background()); platformerrors.CodeOf(err) != platformerrors.CodeInternal {
		t.Fatalf("Apply(bad) error = %v, want internal", err)
	}
	good := fstest.MapFS{
		"0002_broken.sql": &fstest.MapFile{Data: []byte("CREATE TABLE broken (id TEXT);")},
	}
	retry, err := migrationrunner.NewRunner(db, good, migrationrunner.Options{})
	if err != nil {
		t.Fatalf("NewRunner(good) error = %v", err)
	}
	if err := retry.Apply(context.Background()); platformerrors.CodeOf(err) != platformerrors.CodeMigrationDirty {
		t.Fatalf("Apply(retry) error = %v, want migration_dirty", err)
	}
}
