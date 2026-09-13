package sqlite

import (
	"context"
	"database/sql"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"strings"
	"time"
)

// ReserveIdempotency atomically reserves a command key. A same-key/same-hash
// retry returns duplicate=false with CodeConflict so callers do not replay a
// side effect; a different hash is always a hard conflict.
func (s *DB) ReserveIdempotency(ctx context.Context, workspace, key, operation, requestHash string) (bool, error) {
	if ctx == nil || s == nil || s.db == nil {
		return false, platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite store are required")
	}
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(operation) == "" || strings.TrimSpace(requestHash) == "" {
		return false, platformerrors.New(platformerrors.CodeInvalidInput, "idempotency fields are required")
	}
	reserved := false
	err := WithTx(ctx, s.db, func(tx *sql.Tx) error {
		id, err := s.ids.NewID()
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys (id,workspace_id,key,operation_type,operation_id,request_hash,outcome,created_at) VALUES (?,?,?,?,?,?, 'reserved', ?) ON CONFLICT (workspace_id, key) DO NOTHING`, id, workspace, key, operation, operation, requestHash, s.clock.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 1 {
			reserved = true
			return nil
		}
		var existingHash string
		if err := tx.QueryRowContext(ctx, `SELECT request_hash FROM idempotency_keys WHERE workspace_id=? AND key=?`, workspace, key).Scan(&existingHash); err != nil {
			return err
		}
		if existingHash != requestHash {
			return platformerrors.New(platformerrors.CodeConflict, "idempotency key was reused with a different request")
		}
		return nil
	})
	return reserved, err
}
