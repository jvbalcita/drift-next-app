package sqlite

import (
	"context"
	"database/sql"
	"errors"

	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// WithTx gives one application command an explicit transaction boundary. The
// callback must not perform network, device, filesystem, or UI work.
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
	if err := ctx.Err(); err != nil {
		return classifyContext(err)
	}
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
