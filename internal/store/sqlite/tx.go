package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// WithTx gives one application command an explicit write reservation. The
// callback must not perform network, device, filesystem, or UI work. SQLITE_BUSY
// from a deferred-upgrade deadlock is retried so lease/fencing races surface as
// typed conflicts instead of driver lock errors.
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	if ctx == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "SQLite database is required")
	}
	if fn == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "transaction callback is required")
	}
	var last error
	for attempt := 0; attempt < 16; attempt++ {
		if err := ctx.Err(); err != nil {
			return classifyContext(err)
		}
		last = withTxOnce(ctx, db, fn)
		if last == nil || !isBusyError(last) {
			return last
		}
		select {
		case <-ctx.Done():
			return classifyContext(ctx.Err())
		case <-time.After(time.Duration(5*(attempt+1)) * time.Millisecond):
		}
	}
	return platformerrors.Wrap(platformerrors.CodeUnavailable, "SQLite write reservation is busy", last)
}

func withTxOnce(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return classifyContext(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return classifyContext(err)
	}
	if err := tx.Commit(); err != nil {
		return classifyContext(err)
	}
	committed = true
	return nil
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "busy") || strings.Contains(value, "database is locked")
}

func classifyContext(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return platformerrors.Wrap(platformerrors.CodeCanceled, "operation canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return platformerrors.Wrap(platformerrors.CodeDeadlineExceeded, "operation deadline exceeded", err)
	}
	if isBusyError(err) {
		return err
	}
	return err
}

// RequireAffected converts a conditional update that matched no row into a
// stable conflict. Repositories use this for optimistic row-version CAS.
func RequireAffected(result sql.Result, resource string) error {
	n, err := result.RowsAffected()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "read affected row count", err)
	}
	if n == 0 {
		return platformerrors.New(platformerrors.CodeConflict, resource+" changed or no longer exists")
	}
	return nil
}
