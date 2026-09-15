package artifacts

import (
	"context"
	"strings"
	"time"

	"drift.local/drift-next/internal/artifacts/cas"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/clock"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/ids"
)

// MetadataStore persists artifact metadata and references. Implementations must
// never store bytes.
type MetadataStore interface {
	Get(ctx context.Context, workspace organizations.WorkspaceID, id ArtifactID) (Artifact, error)
	GetByHash(ctx context.Context, workspace organizations.WorkspaceID, contentHash string) (Artifact, error)
	List(ctx context.Context, workspace organizations.WorkspaceID, filter ListFilter) ([]Artifact, error)
	Record(ctx context.Context, artifact Artifact, actorType, actorID string) error
	UpdateLifecycle(ctx context.Context, workspace organizations.WorkspaceID, id ArtifactID, from, to State, outcome DeletionOutcome, failure FailureClassification, omitted string, at time.Time, actorType, actorID string) error
	Reference(ctx context.Context, reference Reference, actorType, actorID string) error
	ListReferences(ctx context.Context, workspace organizations.WorkspaceID, ownerType, ownerID string) ([]Reference, error)
	HasActiveProtectedReference(ctx context.Context, workspace organizations.WorkspaceID, id ArtifactID) (bool, error)
	Usage(ctx context.Context, workspace organizations.WorkspaceID) (Usage, error)
	ListCleanupFailed(ctx context.Context, workspace organizations.WorkspaceID) ([]Artifact, error)
	ListOrphanMetadata(ctx context.Context, workspace organizations.WorkspaceID, exists func(contentHash string) (bool, error)) ([]Artifact, error)
}

// Auditor records bounded artifact audit events.
type Auditor interface {
	Record(ctx context.Context, workspace organizations.WorkspaceID, resourceType, resourceID, eventName, actorType, actorID string) error
}

// Authorizer decides whether an actor may read or delete an artifact.
type Authorizer interface {
	Authorize(ctx context.Context, workspace organizations.WorkspaceID, artifactID ArtifactID, actorType, actorID, action string) error
}

// AllowAllAuthorizer is a local-development authorizer seam. Production
// identity/RBAC remains Phase 16.
type AllowAllAuthorizer struct{}

// Authorize always succeeds when actor fields are present.
func (AllowAllAuthorizer) Authorize(_ context.Context, _ organizations.WorkspaceID, _ ArtifactID, actorType, actorID, _ string) error {
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodePolicyDenied, "artifact actor is required")
	}
	return nil
}

// StoreRequest admits and persists one artifact payload.
type StoreRequest struct {
	Workspace      organizations.WorkspaceID
	ID             ArtifactID
	MediaType      string
	Category       Category
	RetentionClass RetentionClass
	SchemaVersion  int
	Payload        []byte
	DeclaredHash   string
	Sensitivity    SensitivityClass
	ActorType      string
	ActorID        string
	OwnerType      string
	OwnerID        string
	ReferenceID    string
}

// StoreResult is the outcome of Store, including omission metadata when refused.
type StoreResult struct {
	Artifact Artifact
	Omitted  bool
	Decision AdmissionDecision
}

// ByteStore is the CAS boundary used by Service.
type ByteStore interface {
	Put(workspaceID, declaredHash string, payload []byte) (string, error)
	Get(workspaceID, contentHash string) ([]byte, error)
	Delete(workspaceID, contentHash string) error
	Verify(workspaceID, contentHash string, expectedSize int64) error
	Exists(workspaceID, contentHash string) (bool, error)
	Root() string
}

// Service coordinates admission, CAS writes, metadata, authorization, and audit.
type Service struct {
	meta   MetadataStore
	bytes  ByteStore
	policy PolicyConfig
	clock  clock.Clock
	ids    ids.IDGenerator
	authz  Authorizer
	audit  Auditor
}

// ServiceOption configures Service.
type ServiceOption func(*Service)

// WithPolicy sets quota/retention seams.
func WithPolicy(policy PolicyConfig) ServiceOption {
	return func(s *Service) { s.policy = policy }
}

// WithClock injects a clock.
func WithClock(source clock.Clock) ServiceOption {
	return func(s *Service) { s.clock = source }
}

// WithIDs injects an ID generator.
func WithIDs(generator ids.IDGenerator) ServiceOption {
	return func(s *Service) { s.ids = generator }
}

// WithAuthorizer injects authorization.
func WithAuthorizer(authorizer Authorizer) ServiceOption {
	return func(s *Service) { s.authz = authorizer }
}

// WithAuditor injects audit recording.
func WithAuditor(auditor Auditor) ServiceOption {
	return func(s *Service) { s.audit = auditor }
}

// NewService constructs the coordinating artifact service.
func NewService(meta MetadataStore, bytes ByteStore, opts ...ServiceOption) (*Service, error) {
	if meta == nil || bytes == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "artifact metadata store and CAS are required")
	}
	service := &Service{
		meta:   meta,
		bytes:  bytes,
		policy: DefaultPolicy(),
		clock:  clock.System{},
		ids:    ids.NewRandom(),
		authz:  AllowAllAuthorizer{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	if err := service.policy.Validate(); err != nil {
		return nil, platformerrors.Wrap(platformerrors.CodeInvalidInput, "artifact policy is invalid", err)
	}
	if service.clock == nil || service.ids == nil || service.authz == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "artifact clock, ids, and authorizer are required")
	}
	return service, nil
}

// Store admits payload, writes CAS bytes, then records metadata. Failures after
// CAS write leave recoverable orphan bytes that health/orphan helpers surface.
func (s *Service) Store(ctx context.Context, request StoreRequest) (StoreResult, error) {
	if ctx == nil {
		return StoreResult{}, platformerrors.New(platformerrors.CodeInvalidInput, "context is required")
	}
	if strings.TrimSpace(string(request.Workspace)) == "" || strings.TrimSpace(request.ActorType) == "" || strings.TrimSpace(request.ActorID) == "" {
		return StoreResult{}, platformerrors.New(platformerrors.CodeInvalidInput, "workspace and actor are required")
	}
	if request.SchemaVersion <= 0 {
		request.SchemaVersion = 1
	}
	if request.Category == "" {
		request.Category = CategoryOther
	}
	if request.RetentionClass == "" {
		request.RetentionClass = RetentionExecutionEvidence
	}
	if !request.Category.Valid() || !request.RetentionClass.Valid() {
		return StoreResult{}, platformerrors.New(platformerrors.CodeInvalidInput, "artifact category or retention is invalid")
	}

	decision := AdmitWithClassification(request.MediaType, request.Payload, request.Sensitivity)
	if !decision.Allowed {
		omission := OmissionMetadata{
			Reason:       decision.OmissionReason,
			Sensitivity:  decision.Sensitivity,
			FailureClass: decision.FailureClass,
			MediaType:    request.MediaType,
			Category:     CategoryOmission,
		}
		artifact, err := s.recordOmission(ctx, request, omission)
		if err != nil {
			return StoreResult{Decision: decision, Omitted: true}, err
		}
		_ = s.auditEvent(ctx, request.Workspace, "artifact", string(artifact.ID), "artifact.admission.rejected", request.ActorType, request.ActorID)
		return StoreResult{Artifact: artifact, Omitted: true, Decision: decision}, nil
	}

	maxBytes := s.policy.EffectiveMaxObjectBytes()
	if int64(len(request.Payload)) > maxBytes {
		return StoreResult{}, platformerrors.New(platformerrors.CodePolicyDenied, "artifact exceeds object size quota")
	}
	if s.policy.MaxWorkspaceBytes > 0 {
		usage, err := s.meta.Usage(ctx, request.Workspace)
		if err != nil {
			return StoreResult{}, err
		}
		if usage.TotalBytes+int64(len(request.Payload)) > s.policy.MaxWorkspaceBytes {
			return StoreResult{}, platformerrors.New(platformerrors.CodePolicyDenied, "artifact exceeds workspace storage quota")
		}
	}

	contentHash, err := s.bytes.Put(string(request.Workspace), request.DeclaredHash, request.Payload)
	if err != nil {
		return StoreResult{}, err
	}

	if existing, getErr := s.meta.GetByHash(ctx, request.Workspace, contentHash); getErr == nil {
		if request.OwnerType != "" && request.OwnerID != "" {
			if refErr := s.ensureReference(ctx, request, existing.ID); refErr != nil {
				return StoreResult{}, refErr
			}
		}
		_ = s.auditEvent(ctx, request.Workspace, "artifact", string(existing.ID), "artifact.store.idempotent", request.ActorType, request.ActorID)
		return StoreResult{Artifact: existing, Decision: decision}, nil
	} else if platformerrors.CodeOf(getErr) != platformerrors.CodeNotFound {
		return StoreResult{}, getErr
	}

	id := request.ID
	if strings.TrimSpace(string(id)) == "" {
		generated, idErr := s.ids.NewID()
		if idErr != nil {
			return StoreResult{}, platformerrors.Wrap(platformerrors.CodeInternal, "generate artifact id", idErr)
		}
		id = ArtifactID(generated)
	}
	now := s.clock.Now().UTC()
	artifact := Artifact{
		ID:             id,
		Workspace:      request.Workspace,
		ContentHash:    contentHash,
		SizeBytes:      int64(len(request.Payload)),
		MediaType:      request.MediaType,
		SchemaVersion:  request.SchemaVersion,
		RetentionClass: request.RetentionClass,
		Category:       request.Category,
		State:          Stored,
		CreatedAt:      now,
		UpdatedAt:      &now,
	}
	if err := s.meta.Record(ctx, artifact, request.ActorType, request.ActorID); err != nil {
		if platformerrors.CodeOf(err) == platformerrors.CodeConflict {
			existing, getErr := s.meta.GetByHash(ctx, request.Workspace, contentHash)
			if getErr == nil {
				return StoreResult{Artifact: existing, Decision: decision}, nil
			}
		}
		return StoreResult{}, err
	}
	if request.OwnerType != "" && request.OwnerID != "" {
		if refErr := s.ensureReference(ctx, request, artifact.ID); refErr != nil {
			return StoreResult{}, refErr
		}
		artifact.State = Referenced
	}
	_ = s.auditEvent(ctx, request.Workspace, "artifact", string(artifact.ID), "artifact.stored", request.ActorType, request.ActorID)
	return StoreResult{Artifact: artifact, Decision: decision}, nil
}

func (s *Service) ensureReference(ctx context.Context, request StoreRequest, artifactID ArtifactID) error {
	refID := request.ReferenceID
	if strings.TrimSpace(refID) == "" {
		generated, err := s.ids.NewID()
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeInternal, "generate reference id", err)
		}
		refID = generated
	}
	return s.meta.Reference(ctx, Reference{
		ID:         refID,
		Workspace:  request.Workspace,
		ArtifactID: artifactID,
		OwnerType:  request.OwnerType,
		OwnerID:    request.OwnerID,
		State:      ReferenceActive,
		CreatedAt:  s.clock.Now().UTC(),
	}, request.ActorType, request.ActorID)
}

func (s *Service) recordOmission(ctx context.Context, request StoreRequest, omission OmissionMetadata) (Artifact, error) {
	if err := omission.Validate(); err != nil {
		return Artifact{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "omission metadata is invalid", err)
	}
	id := request.ID
	if strings.TrimSpace(string(id)) == "" {
		generated, err := s.ids.NewID()
		if err != nil {
			return Artifact{}, platformerrors.Wrap(platformerrors.CodeInternal, "generate omission artifact id", err)
		}
		id = ArtifactID(generated)
	}
	now := s.clock.Now().UTC()
	placeholder := cas.HashBytes([]byte("omission:" + string(id) + ":" + omission.Reason))
	artifact := Artifact{
		ID:                    id,
		Workspace:             request.Workspace,
		ContentHash:           placeholder,
		SizeBytes:             0,
		MediaType:             "application/vnd.drift.omission+json",
		SchemaVersion:         1,
		RetentionClass:        RetentionOperationalHistory,
		Category:              CategoryOmission,
		State:                 Stored,
		CreatedAt:             now,
		UpdatedAt:             &now,
		FailureClassification: omission.FailureClass,
		OmissionReason:        omission.Reason,
		DeletionOutcome:       DeletionOutcomeOmitted,
	}
	if err := s.meta.Record(ctx, artifact, request.ActorType, request.ActorID); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// Get returns authorized metadata.
func (s *Service) Get(ctx context.Context, workspace organizations.WorkspaceID, id ArtifactID, actorType, actorID string) (Artifact, error) {
	if err := s.authz.Authorize(ctx, workspace, id, actorType, actorID, "read"); err != nil {
		_ = s.auditEvent(ctx, workspace, "artifact", string(id), "artifact.authz.denied", actorType, actorID)
		return Artifact{}, err
	}
	artifact, err := s.meta.Get(ctx, workspace, id)
	if err != nil {
		return Artifact{}, err
	}
	_ = s.auditEvent(ctx, workspace, "artifact", string(id), "artifact.metadata.read", actorType, actorID)
	return artifact, nil
}

// Read returns authorized bytes after verifying CAS integrity.
func (s *Service) Read(ctx context.Context, workspace organizations.WorkspaceID, id ArtifactID, actorType, actorID string) ([]byte, Artifact, error) {
	artifact, err := s.Get(ctx, workspace, id, actorType, actorID)
	if err != nil {
		return nil, Artifact{}, err
	}
	if artifact.Category == CategoryOmission || artifact.SizeBytes == 0 && artifact.OmissionReason != "" {
		return nil, artifact, platformerrors.New(platformerrors.CodeNotFound, "artifact bytes were omitted")
	}
	if artifact.State == Deleted {
		return nil, artifact, platformerrors.New(platformerrors.CodeNotFound, "artifact has been deleted")
	}
	payload, err := s.bytes.Get(string(workspace), artifact.ContentHash)
	if err != nil {
		return nil, artifact, err
	}
	_ = s.auditEvent(ctx, workspace, "artifact", string(id), "artifact.bytes.read", actorType, actorID)
	return payload, artifact, nil
}

// List returns filtered metadata after a workspace-scoped authorization check.
func (s *Service) List(ctx context.Context, workspace organizations.WorkspaceID, filter ListFilter, actorType, actorID string) ([]Artifact, error) {
	if err := s.authz.Authorize(ctx, workspace, "", actorType, actorID, "list"); err != nil {
		return nil, err
	}
	return s.meta.List(ctx, workspace, filter)
}

// Delete removes bytes when eligible and unprotected. It never mutates device,
// lease, fencing, or run state.
func (s *Service) Delete(ctx context.Context, workspace organizations.WorkspaceID, id ArtifactID, actorType, actorID string) (DeletionOutcome, error) {
	if err := s.authz.Authorize(ctx, workspace, id, actorType, actorID, "delete"); err != nil {
		_ = s.auditEvent(ctx, workspace, "artifact", string(id), "artifact.authz.denied", actorType, actorID)
		return DeletionOutcomeNone, err
	}
	artifact, err := s.meta.Get(ctx, workspace, id)
	if err != nil {
		return DeletionOutcomeNone, err
	}
	if artifact.State == Deleted {
		return DeletionOutcomeAlreadyDeleted, nil
	}
	protected, err := s.meta.HasActiveProtectedReference(ctx, workspace, id)
	if err != nil {
		return DeletionOutcomeNone, err
	}
	if protected {
		_ = s.auditEvent(ctx, workspace, "artifact", string(id), "artifact.delete.protected", actorType, actorID)
		return DeletionOutcomeSkippedProtected, platformerrors.New(platformerrors.CodeConflict, "artifact has protected references")
	}
	// Audit/security and execution-evidence retention classes are never
	// ordinary-delete targets, including while still in stored/referenced state
	// without an active reference row.
	if artifact.RetentionClass == RetentionAuditSecurity || artifact.RetentionClass == RetentionExecutionEvidence {
		_ = s.auditEvent(ctx, workspace, "artifact", string(id), "artifact.delete.protected", actorType, actorID)
		return DeletionOutcomeSkippedProtected, platformerrors.New(platformerrors.CodeConflict, "protected retention class cannot be deleted")
	}
	if artifact.State != EligibleForDeletion && artifact.State != CleanupFailed {
		if err := s.meta.UpdateLifecycle(ctx, workspace, id, artifact.State, EligibleForDeletion, DeletionOutcomeNone, FailureNone, "", s.clock.Now().UTC(), actorType, actorID); err != nil {
			return DeletionOutcomeNone, err
		}
		artifact.State = EligibleForDeletion
	}
	if err := s.bytes.Delete(string(workspace), artifact.ContentHash); err != nil {
		_ = s.meta.UpdateLifecycle(ctx, workspace, id, artifact.State, CleanupFailed, DeletionOutcomeCleanupFailed, FailureCleanup, "", s.clock.Now().UTC(), actorType, actorID)
		_ = s.auditEvent(ctx, workspace, "artifact", string(id), "artifact.cleanup.failed", actorType, actorID)
		return DeletionOutcomeCleanupFailed, err
	}
	if err := s.meta.UpdateLifecycle(ctx, workspace, id, artifact.State, Deleted, DeletionOutcomeDeleted, FailureNone, "", s.clock.Now().UTC(), actorType, actorID); err != nil {
		return DeletionOutcomeNone, err
	}
	_ = s.auditEvent(ctx, workspace, "artifact", string(id), "artifact.deleted", actorType, actorID)
	return DeletionOutcomeDeleted, nil
}

// Transition applies a validated lifecycle transition.
func (s *Service) Transition(ctx context.Context, workspace organizations.WorkspaceID, id ArtifactID, to State, actorType, actorID string) error {
	if err := s.authz.Authorize(ctx, workspace, id, actorType, actorID, "transition"); err != nil {
		return err
	}
	artifact, err := s.meta.Get(ctx, workspace, id)
	if err != nil {
		return err
	}
	if err := Transition(artifact.State, to); err != nil {
		return platformerrors.Wrap(platformerrors.CodeConflict, "artifact transition rejected", err)
	}
	return s.meta.UpdateLifecycle(ctx, workspace, id, artifact.State, to, DeletionOutcomeNone, FailureNone, "", s.clock.Now().UTC(), actorType, actorID)
}

// Health returns storage health for a workspace.
func (s *Service) Health(ctx context.Context, workspace organizations.WorkspaceID, actorType, actorID string) (StorageHealth, error) {
	if err := s.authz.Authorize(ctx, workspace, "", actorType, actorID, "health"); err != nil {
		return StorageHealth{}, err
	}
	usage, err := s.meta.Usage(ctx, workspace)
	if err != nil {
		return StorageHealth{}, err
	}
	failed, err := s.meta.ListCleanupFailed(ctx, workspace)
	if err != nil {
		return StorageHealth{}, err
	}
	return StorageHealth{
		Workspace:          string(workspace),
		CASRootConfigured:  strings.TrimSpace(s.bytes.Root()) != "",
		ObjectCount:        usage.ObjectCount,
		TotalBytes:         usage.TotalBytes,
		CleanupFailedCount: int64(len(failed)),
		CheckedAt:          s.clock.Now().UTC(),
	}, nil
}

// Usage returns quota usage.
func (s *Service) Usage(ctx context.Context, workspace organizations.WorkspaceID, actorType, actorID string) (Usage, error) {
	if err := s.authz.Authorize(ctx, workspace, "", actorType, actorID, "usage"); err != nil {
		return Usage{}, err
	}
	return s.meta.Usage(ctx, workspace)
}

func (s *Service) auditEvent(ctx context.Context, workspace organizations.WorkspaceID, resourceType, resourceID, eventName, actorType, actorID string) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, workspace, resourceType, resourceID, eventName, actorType, actorID)
}

// Ensure *cas.Store satisfies ByteStore.
var _ ByteStore = (*cas.Store)(nil)
