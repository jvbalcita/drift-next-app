package artifacts

import (
	"fmt"
	"strings"
	"time"
)

// Category classifies artifact content for retention and media routing.
type Category string

const (
	CategoryUnspecified         Category = "unspecified"
	CategoryScreenshot          Category = "screenshot"
	CategoryAnnotatedScreenshot Category = "annotated_screenshot"
	CategoryUITree              Category = "ui_tree"
	CategoryRecording           Category = "recording"
	CategoryOCR                 Category = "ocr"
	CategoryTrace               Category = "trace"
	CategoryOmission            Category = "omission"
	CategoryOther               Category = "other"
)

// Valid reports whether the category is recognized.
func (c Category) Valid() bool {
	switch c {
	case CategoryUnspecified, CategoryScreenshot, CategoryAnnotatedScreenshot, CategoryUITree,
		CategoryRecording, CategoryOCR, CategoryTrace, CategoryOmission, CategoryOther:
		return true
	default:
		return false
	}
}

// FailureClassification is a stable artifact-boundary failure class.
type FailureClassification string

const (
	FailureNone               FailureClassification = ""
	FailureAdmissionRejected  FailureClassification = "admission_rejected"
	FailureQuotaExceeded      FailureClassification = "quota_exceeded"
	FailureHashMismatch       FailureClassification = "hash_mismatch"
	FailureSizeMismatch       FailureClassification = "size_mismatch"
	FailurePathRejected       FailureClassification = "path_rejected"
	FailureUnauthorized       FailureClassification = "unauthorized"
	FailureCleanup            FailureClassification = "cleanup_failed"
	FailureStorage            FailureClassification = "storage_failed"
	FailureProtectedReference FailureClassification = "protected_reference"
	FailureOrphan             FailureClassification = "orphan"
	FailureInternal           FailureClassification = "internal"
)

// Valid reports whether the classification is recognized.
func (f FailureClassification) Valid() bool {
	switch f {
	case FailureNone, FailureAdmissionRejected, FailureQuotaExceeded, FailureHashMismatch,
		FailureSizeMismatch, FailurePathRejected, FailureUnauthorized, FailureCleanup,
		FailureStorage, FailureProtectedReference, FailureOrphan, FailureInternal:
		return true
	default:
		return false
	}
}

// DeletionOutcome records how a deletion or cleanup attempt concluded.
type DeletionOutcome string

const (
	DeletionOutcomeNone             DeletionOutcome = ""
	DeletionOutcomeDeleted          DeletionOutcome = "deleted"
	DeletionOutcomeSkippedProtected DeletionOutcome = "skipped_protected"
	DeletionOutcomeCleanupFailed    DeletionOutcome = "cleanup_failed"
	DeletionOutcomeAlreadyDeleted   DeletionOutcome = "already_deleted"
	DeletionOutcomeOmitted          DeletionOutcome = "omitted"
)

// Valid reports whether the deletion outcome is recognized.
func (d DeletionOutcome) Valid() bool {
	switch d {
	case DeletionOutcomeNone, DeletionOutcomeDeleted, DeletionOutcomeSkippedProtected,
		DeletionOutcomeCleanupFailed, DeletionOutcomeAlreadyDeleted, DeletionOutcomeOmitted:
		return true
	default:
		return false
	}
}

// Usage summarizes workspace artifact quota consumption.
type Usage struct {
	Workspace     string
	ObjectCount   int64
	TotalBytes    int64
	CleanupFailed int64
	OrphanMeta    int64
}

// StorageHealth is a bounded health snapshot for operators.
type StorageHealth struct {
	Workspace          string
	CASRootConfigured  bool
	ObjectCount        int64
	TotalBytes         int64
	CleanupFailedCount int64
	CheckedAt          time.Time
}

// ListFilter selects artifact metadata rows within one workspace.
type ListFilter struct {
	States     []State
	Categories []Category
	Retention  []RetentionClass
	Limit      int
}

// OmissionMetadata is recorded when admission refuses unsafe bytes.
type OmissionMetadata struct {
	Reason       string
	Sensitivity  SensitivityClass
	FailureClass FailureClassification
	MediaType    string
	Category     Category
}

// Validate checks omission metadata bounds.
func (o OmissionMetadata) Validate() error {
	if strings.TrimSpace(o.Reason) == "" || len(o.Reason) > 512 {
		return fmt.Errorf("omission reason is required and bounded")
	}
	if !o.FailureClass.Valid() || o.FailureClass == FailureNone {
		return fmt.Errorf("omission failure classification is required")
	}
	if o.Category != "" && !o.Category.Valid() {
		return fmt.Errorf("omission category is invalid")
	}
	return nil
}
