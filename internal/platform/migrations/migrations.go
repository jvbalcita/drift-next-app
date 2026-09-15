// Package migrations applies immutable, forward-only SQLite migrations from
// an injected filesystem.
package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	platformclock "drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"modernc.org/sqlite"
)

const (
	// LedgerTableName is the durable metadata table owned by this package.
	LedgerTableName = "drift_schema_migrations"

	minimumSQLiteMigrationVersion int64 = 2
	defaultBusyTimeout                  = 5 * time.Second
	maximumBusyTimeout                  = 30 * time.Second
)

var migrationFilename = regexp.MustCompile(`^([0-9]{4})_([a-z0-9][a-z0-9_]*)\.sql$`)

const createLedgerSQL = `
CREATE TABLE IF NOT EXISTS drift_schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    checksum TEXT NOT NULL,
    applied_at TEXT NOT NULL,
    dirty INTEGER NOT NULL CHECK (dirty IN (0, 1))
);`

// Options controls the injectable dependencies and bounded lock wait used by
// a Runner. A zero Options value uses the UTC system clock and a five-second
// busy timeout.
type Options struct {
	Clock       platformclock.Clock
	BusyTimeout time.Duration
}

// OpenOptions controls the SQLite connection factory. A zero BusyTimeout uses
// the same five-second default as Runner.
type OpenOptions struct {
	BusyTimeout time.Duration
}

// Open opens a pure-Go SQLite database with the local-first connection
// invariants owned by this package. Every connection enables WAL mode,
// foreign-key enforcement, and the bounded busy timeout through the modernc
// driver DSN. The caller owns the returned database and must close it.
func Open(ctx context.Context, dsn string, options OpenOptions) (*sql.DB, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite database DSN is required")
	}
	busyTimeout, err := normalizeBusyTimeout(options.BusyTimeout)
	if err != nil {
		return nil, err
	}
	configuredDSN, err := sqliteDSNWithPragmas(dsn, busyTimeout)
	if err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "configure SQLite database DSN", err)
	}
	connector, err := sqlite.NewConnector(configuredDSN)
	if err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "create SQLite connector", err)
	}
	db := sql.OpenDB(connector)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, wrapContextOrError(ctx, platformerrors.CodeInternal, "open SQLite database", err)
	}
	return db, nil
}

// Runner applies SQL migration files to one supplied database connection
// pool. The runner never opens a database or reads process environment.
type Runner struct {
	db          *sql.DB
	source      fs.FS
	clock       platformclock.Clock
	busyTimeout time.Duration

	// afterLedgerRead is nil in production and only lets package tests hold two
	// real runners at the empty-ledger race boundary.
	afterLedgerRead func()
}

type migration struct {
	version  int64
	name     string
	filename string
	sql      []byte
	checksum string
}

type ledgerEntry struct {
	version   int64
	name      string
	checksum  string
	appliedAt string
	dirty     bool
}

// NewRunner creates a migration runner over db and source. The source must
// contain SQLite migrations numbered from 0002 onward; the historical
// PostgreSQL 0001 bootstrap is deliberately rejected.
func NewRunner(db *sql.DB, source fs.FS, options Options) (*Runner, error) {
	if db == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "migration database is required")
	}
	if source == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "migration filesystem is required")
	}
	if options.Clock == nil {
		options.Clock = platformclock.System{}
	}
	busyTimeout, err := normalizeBusyTimeout(options.BusyTimeout)
	if err != nil {
		return nil, err
	}
	return &Runner{
		db:          db,
		source:      source,
		clock:       options.Clock,
		busyTimeout: busyTimeout,
	}, nil
}

// Apply validates and applies every pending migration in ascending version
// order. A dirty ledger row always blocks further application until Repair is
// called explicitly for that version.
func (r *Runner) Apply(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	migrations, err := loadMigrations(ctx, r.source)
	if err != nil {
		return err
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "get migration database connection", err)
	}
	defer conn.Close()
	if err := r.configureConnection(ctx, conn); err != nil {
		return err
	}
	if err := r.bootstrapLedger(ctx, conn); err != nil {
		return err
	}
	ledger, err := readLedger(ctx, conn)
	if err != nil {
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "read migration ledger", err)
	}
	if err := validateLedger(ledger, migrations); err != nil {
		return err
	}
	if r.afterLedgerRead != nil {
		r.afterLedgerRead()
	}

	for _, next := range migrations {
		if _, applied := ledger[next.version]; applied {
			continue
		}
		markedDirty, err := r.markDirty(ctx, conn, next)
		if err != nil {
			return err
		}
		if !markedDirty {
			ledger[next.version] = ledgerEntry{
				version:  next.version,
				name:     next.name,
				checksum: next.checksum,
			}
			continue
		}
		if err := r.applyOne(ctx, conn, next); err != nil {
			return err
		}
		ledger[next.version] = ledgerEntry{
			version:   next.version,
			name:      next.name,
			checksum:  next.checksum,
			appliedAt: r.clock.Now().UTC().Format(time.RFC3339Nano),
		}
	}
	return nil
}

// Repair explicitly removes one dirty, failed migration from the applied set
// so the unchanged file can be retried after the operator has corrected the
// external failure. It never edits migration files or silently repairs clean
// ledger state.
func (r *Runner) Repair(ctx context.Context, version int64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if version < minimumSQLiteMigrationVersion {
		return platformerrors.New(platformerrors.CodeInvalidInput, "only SQLite migration versions from 0002 onward can be repaired")
	}
	migrations, err := loadMigrations(ctx, r.source)
	if err != nil {
		return err
	}
	byVersion := make(map[int64]migration, len(migrations))
	for _, item := range migrations {
		byVersion[item.version] = item
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "get migration database connection", err)
	}
	defer conn.Close()
	if err := r.configureConnection(ctx, conn); err != nil {
		return err
	}
	if err := r.bootstrapLedger(ctx, conn); err != nil {
		return err
	}
	if err := beginImmediate(ctx, conn); err != nil {
		return err
	}
	entry, err := scanLedgerEntry(conn.QueryRowContext(ctx, `
SELECT version, name, checksum, applied_at, dirty
FROM drift_schema_migrations
WHERE version = ?`, version))
	if err != nil {
		r.rollback(conn)
		if ctxErr := contextErrorWithCause(ctx, err); ctxErr != nil {
			return ctxErr
		}
		if stderrors.Is(err, sql.ErrNoRows) {
			return platformerrors.New(platformerrors.CodeInvalidInput, "migration version is not dirty")
		}
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "read migration before repair", err)
	}
	if !entry.dirty {
		r.rollback(conn)
		return platformerrors.New(platformerrors.CodeInvalidInput, "migration version is not dirty")
	}
	item, ok := byVersion[version]
	if !ok || entry.name != item.name || entry.checksum != item.checksum {
		r.rollback(conn)
		return platformerrors.New(platformerrors.CodeMigrationChecksumMismatch, "dirty migration does not match the current immutable file")
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM drift_schema_migrations WHERE version = ? AND dirty = 1`, version); err != nil {
		r.rollback(conn)
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "remove dirty migration during repair", err)
	}
	if err := commit(ctx, conn); err != nil {
		return err
	}
	return nil
}

func (r *Runner) configureConnection(ctx context.Context, conn *sql.Conn) error {
	milliseconds := (r.busyTimeout + time.Millisecond - 1) / time.Millisecond
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", milliseconds)); err != nil {
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "configure migration busy timeout", err)
	}
	return nil
}

func (r *Runner) bootstrapLedger(ctx context.Context, conn *sql.Conn) error {
	if err := beginImmediate(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, createLedgerSQL); err != nil {
		r.rollback(conn)
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "create migration ledger", err)
	}
	return commit(ctx, conn)
}

func (r *Runner) markDirty(ctx context.Context, conn *sql.Conn, item migration) (bool, error) {
	if err := beginImmediate(ctx, conn); err != nil {
		return false, err
	}
	_, err := conn.ExecContext(ctx, `
INSERT INTO drift_schema_migrations (version, name, checksum, applied_at, dirty)
VALUES (?, ?, ?, ?, 1)`, item.version, item.name, item.checksum, r.clock.Now().UTC().Format(time.RFC3339Nano))
	if err == nil {
		return true, commit(ctx, conn)
	}
	if !isSQLiteConstraint(err) {
		r.rollback(conn)
		return false, wrapContextOrError(ctx, platformerrors.CodeInternal, "mark migration dirty", err)
	}

	entry, readErr := scanLedgerEntry(conn.QueryRowContext(ctx, `
SELECT version, name, checksum, applied_at, dirty
FROM drift_schema_migrations
WHERE version = ?`, item.version))
	if readErr != nil {
		r.rollback(conn)
		return false, wrapContextOrError(ctx, platformerrors.CodeInternal, "read migration after duplicate dirty marker", readErr)
	}
	if entry.dirty {
		r.rollback(conn)
		if ctxErr := contextErrorWithCause(ctx, err); ctxErr != nil {
			return false, ctxErr
		}
		return false, platformerrors.Wrap(platformerrors.CodeMigrationDirty, "another migration attempt is already dirty", err)
	}
	if entry.name != item.name || entry.checksum != item.checksum {
		r.rollback(conn)
		return false, platformerrors.New(platformerrors.CodeMigrationChecksumMismatch, "applied migration version does not match its immutable file")
	}
	if err := commit(ctx, conn); err != nil {
		return false, err
	}
	return false, nil
}

func (r *Runner) applyOne(ctx context.Context, conn *sql.Conn, item migration) error {
	if err := beginImmediate(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, string(item.sql)); err != nil {
		r.rollback(conn)
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "migration SQL failed", fmt.Errorf("%s: %w", item.filename, err))
	}
	result, err := conn.ExecContext(ctx, `
UPDATE drift_schema_migrations
SET checksum = ?, name = ?, applied_at = ?, dirty = 0
WHERE version = ? AND dirty = 1`, item.checksum, item.name, r.clock.Now().UTC().Format(time.RFC3339Nano), item.version)
	if err != nil {
		r.rollback(conn)
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "clear migration dirty state", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		r.rollback(conn)
		return wrapContextOrError(ctx, platformerrors.CodeInternal, "check migration ledger update", err)
	}
	if affected != 1 {
		r.rollback(conn)
		if ctxErr := contextError(ctx); ctxErr != nil {
			return ctxErr
		}
		return platformerrors.New(platformerrors.CodeInternal, "migration ledger update affected an unexpected row count")
	}
	return commit(ctx, conn)
}

func (r *Runner) rollback(conn *sql.Conn) {
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
}

func loadMigrations(ctx context.Context, source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "read migration filesystem", err)
	}
	migrations := make([]migration, 0, len(entries))
	seen := make(map[int64]string)
	for _, entry := range entries {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		match := migrationFilename.FindStringSubmatch(entry.Name())
		if len(match) != 3 {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "migration SQL filenames must use NNNN_name.sql")
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || version < minimumSQLiteMigrationVersion {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite migrations must start at 0002; the historical PostgreSQL 0001 is not SQLite input")
		}
		if previous, duplicate := seen[version]; duplicate {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, fmt.Sprintf("duplicate migration version %04d in %s and %s", version, previous, entry.Name()))
		}
		data, err := fs.ReadFile(source, entry.Name())
		if err != nil {
			return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "read migration SQL", err)
		}
		checksumBytes := sha256.Sum256(data)
		seen[version] = entry.Name()
		migrations = append(migrations, migration{
			version:  version,
			name:     match[2],
			filename: entry.Name(),
			sql:      append([]byte(nil), data...),
			checksum: hex.EncodeToString(checksumBytes[:]),
		})
	}
	sort.Slice(migrations, func(left, right int) bool {
		return migrations[left].version < migrations[right].version
	})
	return migrations, nil
}

func readLedger(ctx context.Context, conn *sql.Conn) (map[int64]ledgerEntry, error) {
	rows, err := conn.QueryContext(ctx, `
SELECT version, name, checksum, applied_at, dirty
FROM drift_schema_migrations
ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ledger := make(map[int64]ledgerEntry)
	for rows.Next() {
		entry, err := scanLedgerEntry(rows)
		if err != nil {
			return nil, err
		}
		ledger[entry.version] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ledger, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLedgerEntry(scanner rowScanner) (ledgerEntry, error) {
	var entry ledgerEntry
	var dirty int64
	if err := scanner.Scan(&entry.version, &entry.name, &entry.checksum, &entry.appliedAt, &dirty); err != nil {
		return ledgerEntry{}, err
	}
	entry.dirty = dirty != 0
	return entry, nil
}

func validateLedger(ledger map[int64]ledgerEntry, migrations []migration) error {
	byVersion := make(map[int64]migration, len(migrations))
	for _, item := range migrations {
		byVersion[item.version] = item
	}
	for version, entry := range ledger {
		if entry.dirty {
			return platformerrors.New(platformerrors.CodeMigrationDirty, fmt.Sprintf("migration version %04d is dirty; explicit repair is required", version))
		}
		item, ok := byVersion[version]
		if !ok || item.name != entry.name || item.checksum != entry.checksum {
			return platformerrors.New(platformerrors.CodeMigrationChecksumMismatch, fmt.Sprintf("applied migration version %04d does not match its immutable file", version))
		}
	}
	return nil
}

func beginImmediate(ctx context.Context, conn *sql.Conn) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if ctxErr := contextErrorWithCause(ctx, err); ctxErr != nil {
			return ctxErr
		}
		if isSQLiteBusy(err) {
			return platformerrors.Wrap(platformerrors.CodeMigrationLocked, "migration database lock was not acquired before the bounded timeout", err)
		}
		return platformerrors.Wrap(platformerrors.CodeInternal, "begin immediate migration transaction", err)
	}
	return nil
}

func commit(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		if ctxErr := contextErrorWithCause(ctx, err); ctxErr != nil {
			return ctxErr
		}
		return platformerrors.Wrap(platformerrors.CodeInternal, "commit migration transaction", err)
	}
	return nil
}

func normalizeBusyTimeout(timeout time.Duration) (time.Duration, error) {
	if timeout == 0 {
		return defaultBusyTimeout, nil
	}
	if timeout < time.Millisecond || timeout > maximumBusyTimeout {
		return 0, platformerrors.New(platformerrors.CodeInvalidInput, "SQLite busy timeout must be between one millisecond and thirty seconds")
	}
	return timeout, nil
}

func sqliteDSNWithPragmas(dsn string, timeout time.Duration) (string, error) {
	pragmas := url.Values{}
	pragmas.Set("_txlock", "immediate")
	pragmas.Add("_pragma", "journal_mode=wal")
	pragmas.Add("_pragma", "foreign_keys=on")
	pragmas.Add("_pragma", fmt.Sprintf("busy_timeout=%d", timeout/time.Millisecond))
	if !strings.HasPrefix(strings.ToLower(dsn), "file:") {
		separator := "?"
		if strings.ContainsRune(dsn, '?') {
			separator = "&"
		}
		return dsn + separator + pragmas.Encode(), nil
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for key, values := range pragmas {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func contextError(ctx context.Context) error {
	return contextErrorWithCause(ctx, nil)
}

func contextErrorWithCause(ctx context.Context, cause error) error {
	if ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "migration context is required")
	}
	err := ctx.Err()
	if err == nil {
		err = cause
	}
	switch {
	case stderrors.Is(err, context.Canceled):
		return platformerrors.Wrap(platformerrors.CodeCanceled, "migration operation canceled", err)
	case stderrors.Is(err, context.DeadlineExceeded):
		return platformerrors.Wrap(platformerrors.CodeDeadlineExceeded, "migration operation deadline exceeded", err)
	default:
		return nil
	}
}

func wrapContextOrError(ctx context.Context, code platformerrors.Code, message string, cause error) error {
	if ctxErr := contextErrorWithCause(ctx, cause); ctxErr != nil {
		return ctxErr
	}
	return platformerrors.Wrap(code, message, cause)
}

func isSQLiteBusy(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "database is busy") ||
		strings.Contains(message, "database busy")
}

func isSQLiteConstraint(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "constraint") || strings.Contains(message, "unique")
}
