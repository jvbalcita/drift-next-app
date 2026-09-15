// Package backup verifies SQLite metadata and CAS bytes as one restore unit.
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"drift.local/drift-next/internal/artifacts"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// ByteProbe verifies CAS object presence without exposing paths to callers.
type ByteProbe interface {
	Exists(workspaceID, contentHash string) (bool, error)
	Verify(workspaceID, contentHash string, expectedSize int64) error
}

// MetadataProbe reads artifact and control-plane restore invariants.
type MetadataProbe interface {
	List(ctx context.Context, workspace organizations.WorkspaceID, filter artifacts.ListFilter) ([]artifacts.Artifact, error)
	ListOrphanMetadata(ctx context.Context, workspace organizations.WorkspaceID, exists func(contentHash string) (bool, error)) ([]artifacts.Artifact, error)
}

// LeaseProbe reports whether any lease should be refused after restore.
type LeaseProbe interface {
	CountResumableStale(ctx context.Context, workspace organizations.WorkspaceID, now time.Time) (int, error)
}

// MigrationProbe validates the migration ledger after restore.
type MigrationProbe interface {
	VerifyLedger(ctx context.Context) error
}

// Report is the restore/export verification result.
type Report struct {
	Workspace          string
	ArtifactCount      int
	VerifiedBytes      int
	OrphanMetadata     int
	StaleLeasesRefused int
	ForeignKeysOK      bool
	MigrationLedgerOK  bool
	CheckedAt          time.Time
	Errors             []string
}

// Verifier checks database and CAS consistency for one workspace.
type Verifier struct {
	DB         *sql.DB
	Meta       MetadataProbe
	Bytes      ByteProbe
	Leases     LeaseProbe
	Migrations MigrationProbe
	Now        func() time.Time
}

// VerifyExportRestore runs FK, migration, hash, orphan, and stale-lease checks.
// Stale leases are counted and must not resume; this method never mutates them.
func (v *Verifier) VerifyExportRestore(ctx context.Context, workspace organizations.WorkspaceID) (Report, error) {
	if ctx == nil || v == nil || v.DB == nil || v.Meta == nil || v.Bytes == nil {
		return Report{}, platformerrors.New(platformerrors.CodeInvalidInput, "backup verifier dependencies are required")
	}
	now := time.Now().UTC()
	if v.Now != nil {
		now = v.Now().UTC()
	}
	report := Report{Workspace: string(workspace), CheckedAt: now}

	rows, err := v.DB.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("foreign_key_check query: %v", err))
	} else {
		violations := 0
		for rows.Next() {
			violations++
		}
		_ = rows.Close()
		report.ForeignKeysOK = violations == 0 && rows.Err() == nil
		if violations > 0 {
			report.Errors = append(report.Errors, fmt.Sprintf("%d foreign key violations", violations))
		}
	}

	if v.Migrations != nil {
		if err := v.Migrations.VerifyLedger(ctx); err != nil {
			report.Errors = append(report.Errors, err.Error())
		} else {
			report.MigrationLedgerOK = true
		}
	} else {
		report.MigrationLedgerOK = true
	}

	listed, err := v.Meta.List(ctx, workspace, artifacts.ListFilter{})
	if err != nil {
		return report, err
	}
	report.ArtifactCount = len(listed)
	for _, artifact := range listed {
		if artifact.Category == artifacts.CategoryOmission || artifact.State == artifacts.Deleted || artifact.SizeBytes == 0 {
			continue
		}
		if err := v.Bytes.Verify(string(workspace), artifact.ContentHash, artifact.SizeBytes); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("artifact %s: %v", artifact.ID, err))
			continue
		}
		report.VerifiedBytes++
	}

	orphans, err := v.Meta.ListOrphanMetadata(ctx, workspace, func(contentHash string) (bool, error) {
		return v.Bytes.Exists(string(workspace), contentHash)
	})
	if err != nil {
		return report, err
	}
	report.OrphanMetadata = len(orphans)
	if len(orphans) > 0 {
		report.Errors = append(report.Errors, fmt.Sprintf("%d orphan metadata rows without CAS bytes", len(orphans)))
	}

	if v.Leases != nil {
		stale, leaseErr := v.Leases.CountResumableStale(ctx, workspace, now)
		if leaseErr != nil {
			report.Errors = append(report.Errors, leaseErr.Error())
		} else {
			report.StaleLeasesRefused = stale
			if stale > 0 {
				report.Errors = append(report.Errors, fmt.Sprintf("%d stale leases must not resume after restore", stale))
			}
		}
	}

	if len(report.Errors) > 0 {
		return report, platformerrors.New(platformerrors.CodePreconditionFailed, "backup/restore verification failed")
	}
	return report, nil
}
