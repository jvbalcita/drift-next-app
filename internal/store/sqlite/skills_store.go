package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/organizations"
	platformerrors "drift.local/drift-next/internal/platform/errors"
	"drift.local/drift-next/internal/platform/redaction"
	"drift.local/drift-next/internal/recordings"
	"drift.local/drift-next/internal/skills"
	"drift.local/drift-next/internal/workflows"
)

// SkillRepository exposes immutable skill, version, and shared-brain
// projections. Version definitions are decoded into typed workflow steps.
type SkillRepository struct{ store *DB }

func NewSkillRepository(store *DB) *SkillRepository { return &SkillRepository{store: store} }

func (r *SkillRepository) Get(ctx context.Context, workspace organizations.WorkspaceID, id skills.SkillID) (skills.Skill, error) {
	if err := validateSkillRepository(ctx, r, workspace, string(id)); err != nil {
		return skills.Skill{}, err
	}
	var result skills.Skill
	var state, created, updated string
	err := r.store.db.QueryRowContext(ctx, `SELECT id,workspace_id,name,state,created_at,updated_at FROM skills WHERE workspace_id=? AND id=?`, workspace, id).Scan(&result.ID, &result.Workspace, &result.Name, &state, &created, &updated)
	if err == sql.ErrNoRows {
		return result, platformerrors.New(platformerrors.CodeNotFound, "skill not found")
	}
	if err != nil {
		return result, classifyContext(err)
	}
	result.State = skills.State(state)
	result.CreatedAt = parseTimeOrZero(created)
	result.UpdatedAt = parseTimeOrZero(updated)
	return result, nil
}

func (r *SkillRepository) List(ctx context.Context, workspace organizations.WorkspaceID) ([]skills.Skill, error) {
	if err := validateSkillRepository(ctx, r, workspace, "workspace"); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,name,state,created_at,updated_at FROM skills WHERE workspace_id=? ORDER BY name,id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]skills.Skill, 0)
	for rows.Next() {
		var skill skills.Skill
		var state, created, updated string
		if err := rows.Scan(&skill.ID, &skill.Workspace, &skill.Name, &state, &created, &updated); err != nil {
			return nil, err
		}
		skill.State = skills.State(state)
		skill.CreatedAt = parseTimeOrZero(created)
		skill.UpdatedAt = parseTimeOrZero(updated)
		result = append(result, skill)
	}
	return result, classifyContext(rows.Err())
}

func (r *SkillRepository) GetVersion(ctx context.Context, workspace organizations.WorkspaceID, id skills.SkillVersionID) (skills.SkillVersion, error) {
	if err := validateSkillRepository(ctx, r, workspace, string(id)); err != nil {
		return skills.SkillVersion{}, err
	}
	return loadSkillVersion(ctx, r.store.db, workspace, id)
}

func (r *SkillRepository) ListVersions(ctx context.Context, workspace organizations.WorkspaceID, skillID skills.SkillID) ([]skills.SkillVersion, error) {
	if err := validateSkillRepository(ctx, r, workspace, string(skillID)); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id FROM skill_versions WHERE workspace_id=? AND skill_id=? ORDER BY version DESC,id`, workspace, skillID)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	ids := make([]skills.SkillVersionID, 0)
	for rows.Next() {
		var id skills.SkillVersionID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := classifyContext(rows.Err()); err != nil {
		return nil, err
	}
	result := make([]skills.SkillVersion, 0, len(ids))
	for _, id := range ids {
		version, err := loadSkillVersion(ctx, r.store.db, workspace, id)
		if err != nil {
			return nil, err
		}
		result = append(result, version)
	}
	return result, nil
}

func (r *SkillRepository) GetBrainKnowledge(ctx context.Context, workspace organizations.WorkspaceID, id string) (skills.BrainKnowledge, error) {
	if err := validateSkillRepository(ctx, r, workspace, id); err != nil {
		return skills.BrainKnowledge{}, err
	}
	var result skills.BrainKnowledge
	var state string
	err := r.store.db.QueryRowContext(ctx, `SELECT id,workspace_id,knowledge_key,version,state,knowledge_json,source_skill_version_id FROM shared_brain_knowledge WHERE workspace_id=? AND id=?`, workspace, id).Scan(&result.ID, &result.Workspace, &result.Key, &result.Version, &state, &result.KnowledgeJSON, &result.SourceSkillVersion)
	if err == sql.ErrNoRows {
		return result, platformerrors.New(platformerrors.CodeNotFound, "shared-brain knowledge not found")
	}
	if err != nil {
		return result, classifyContext(err)
	}
	result.State = skills.State(state)
	return result, nil
}

func (r *SkillRepository) ListBrainKnowledge(ctx context.Context, workspace organizations.WorkspaceID) ([]skills.BrainKnowledge, error) {
	if err := validateSkillRepository(ctx, r, workspace, "workspace"); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT id,workspace_id,knowledge_key,version,state,knowledge_json,source_skill_version_id FROM shared_brain_knowledge WHERE workspace_id=? ORDER BY knowledge_key,version,id`, workspace)
	if err != nil {
		return nil, classifyContext(err)
	}
	defer rows.Close()
	result := make([]skills.BrainKnowledge, 0)
	for rows.Next() {
		var knowledge skills.BrainKnowledge
		var state string
		if err := rows.Scan(&knowledge.ID, &knowledge.Workspace, &knowledge.Key, &knowledge.Version, &state, &knowledge.KnowledgeJSON, &knowledge.SourceSkillVersion); err != nil {
			return nil, err
		}
		knowledge.State = skills.State(state)
		result = append(result, knowledge)
	}
	return result, classifyContext(rows.Err())
}

func validateSkillRepository(ctx context.Context, repository *SkillRepository, workspace organizations.WorkspaceID, id string) error {
	if ctx == nil || repository == nil || repository.store == nil || repository.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite repository are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "skill resource ID is required")
	}
	return nil
}

// SkillService owns skill admission and explicit promotion. Definition bytes
// are generated from typed steps; callers cannot supply executable JSON.
type SkillService struct{ store *DB }

func NewSkillService(store *DB) *SkillService { return &SkillService{store: store} }

func (s *SkillService) Create(ctx context.Context, skill skills.Skill, actorType, actorID string) (skills.Skill, error) {
	if err := validateSkillService(ctx, s, skill.Workspace, actorType, actorID); err != nil {
		return skills.Skill{}, err
	}
	if skill.State == "" {
		skill.State = skills.Draft
	}
	if skill.State != skills.Draft {
		return skills.Skill{}, platformerrors.New(platformerrors.CodeInvalidInput, "new skills must be drafts")
	}
	if strings.TrimSpace(string(skill.ID)) == "" {
		id, err := s.store.ids.NewID()
		if err != nil {
			return skills.Skill{}, platformerrors.Wrap(platformerrors.CodeInternal, "generate skill ID", err)
		}
		skill.ID = skills.SkillID(id)
	}
	now := s.store.clock.Now().UTC()
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	if skill.UpdatedAt.IsZero() {
		skill.UpdatedAt = skill.CreatedAt
	}
	if err := skill.Validate(); err != nil {
		return skills.Skill{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "skill is invalid", err)
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO skills (id,workspace_id,name,state,created_at,updated_at) VALUES (?,?,?,?,?,?)`, skill.ID, skill.Workspace, skill.Name, skill.State, skill.CreatedAt.UTC().Format(time.RFC3339Nano), skill.UpdatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(skill.Workspace), "skill", string(skill.ID), "skill.created", actorType, actorID)
	})
	return skill, err
}

func (s *SkillService) CreateVersion(ctx context.Context, workspace organizations.WorkspaceID, version skills.SkillVersion, actorType, actorID string) (skills.SkillVersion, error) {
	if err := validateSkillServiceInput(ctx, s, workspace, actorType, actorID); err != nil {
		return skills.SkillVersion{}, err
	}
	version.Workspace = workspace
	if version.State == "" {
		version.State = skills.Draft
	}
	if version.Trust == "" {
		version.Trust = skills.TrustUnreviewed
	}
	if version.State != skills.Draft || version.Trust != skills.TrustUnreviewed {
		return version, platformerrors.New(platformerrors.CodeInvalidInput, "new skill versions must be unreviewed drafts")
	}
	if version.Manifest.Risk == "" {
		version.Manifest.Risk = action.RiskLow
	}
	if version.Manifest.Retry == "" {
		version.Manifest.Retry = action.RetrySafe
	}
	if strings.TrimSpace(string(version.ID)) == "" {
		id, err := s.store.ids.NewID()
		if err != nil {
			return version, platformerrors.Wrap(platformerrors.CodeInternal, "generate skill version ID", err)
		}
		version.ID = skills.SkillVersionID(id)
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = s.store.clock.Now().UTC()
	}
	if err := version.Validate(); err != nil {
		return version, platformerrors.Wrap(platformerrors.CodeInvalidInput, "skill version is invalid", err)
	}
	definition, err := version.DefinitionJSON()
	if err != nil {
		return version, platformerrors.Wrap(platformerrors.CodeInvalidInput, "skill definition is invalid", err)
	}
	manifest, err := version.ManifestJSON()
	if err != nil {
		return version, platformerrors.Wrap(platformerrors.CodeInvalidInput, "skill manifest is invalid", err)
	}
	capabilities, err := jsonArray(version.Manifest.RequestedCapabilities)
	if err != nil {
		return version, platformerrors.Wrap(platformerrors.CodeInternal, "encode skill capabilities", err)
	}
	fixtures, err := jsonArray(version.Manifest.Fixtures)
	if err != nil {
		return version, platformerrors.Wrap(platformerrors.CodeInternal, "encode skill fixtures", err)
	}
	if !safeSkillJSON(definition) || !safeSkillJSON(manifest) || !safeSkillJSON(capabilities) || !safeSkillJSON(fixtures) {
		return version, platformerrors.New(platformerrors.CodeInvalidInput, "skill metadata is sensitive")
	}
	return version, WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		var skillState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM skills WHERE workspace_id=? AND id=?`, workspace, version.SkillID).Scan(&skillState); err == sql.ErrNoRows {
			return platformerrors.New(platformerrors.CodeNotFound, "skill not found")
		} else if err != nil {
			return err
		}
		if skills.State(skillState) == skills.Retired {
			return platformerrors.New(platformerrors.CodeConflict, "retired skill cannot receive a version")
		}
		if version.SourceRecordingSession != "" {
			var state string
			var deleted sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT state,deleted_at FROM recording_sessions WHERE workspace_id=? AND id=?`, workspace, version.SourceRecordingSession).Scan(&state, &deleted); err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "source recording session not found")
			} else if err != nil {
				return err
			}
			if state != string(recordings.SessionCompleted) || deleted.Valid {
				return platformerrors.New(platformerrors.CodeConflict, "skill source recording must be an undeleted completed session")
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO skill_versions (id,workspace_id,skill_id,version,state,definition_json,capabilities_json,trust_state,source_recording_session_id,created_at,manifest_json,fixtures_json,risk_class,retry_class,reviewer_id,reviewed_at,rollback_of) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, version.ID, workspace, version.SkillID, version.Version, version.State, definition, capabilities, version.Trust, nullableString(string(version.SourceRecordingSession)), version.CreatedAt.UTC().Format(time.RFC3339Nano), manifest, fixtures, version.Manifest.Risk, version.Manifest.Retry, version.ReviewerID, nullableTime(version.ReviewedAt), nullableString(string(version.RollbackOf)))
		if err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "skill_version", string(version.ID), "skill.version_created", actorType, actorID)
	})
}

func (s *SkillService) ReviewVersion(ctx context.Context, workspace organizations.WorkspaceID, id skills.SkillVersionID, reviewer, reason string) (skills.SkillVersion, skills.Promotion, error) {
	if err := validateSkillServiceInput(ctx, s, workspace, "operator", reviewer); err != nil {
		return skills.SkillVersion{}, skills.Promotion{}, err
	}
	if err := validatePromotionText(reason); err != nil {
		return skills.SkillVersion{}, skills.Promotion{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "skill review reason is invalid", err)
	}
	var result skills.SkillVersion
	var promotion skills.Promotion
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadSkillVersion(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		result, promotion, err = skills.Review(current, reviewer, reason, s.store.clock.Now())
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "skill version cannot be reviewed", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE skill_versions SET state=?,trust_state=?,reviewer_id=?,reviewed_at=? WHERE workspace_id=? AND id=? AND state='draft' AND trust_state='unreviewed'`, result.State, result.Trust, result.ReviewerID, nullableTime(result.ReviewedAt), workspace, id)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(updated, "skill version"); err != nil {
			return err
		}
		if err := s.insertPromotion(ctx, tx, &promotion); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "skill_version", string(id), "skill.version_reviewed", "operator", reviewer)
	})
	return result, promotion, err
}

func (s *SkillService) PublishVersion(ctx context.Context, workspace organizations.WorkspaceID, id skills.SkillVersionID, actorID, reason string) (skills.SkillVersion, skills.Promotion, error) {
	if err := validateSkillServiceInput(ctx, s, workspace, "operator", actorID); err != nil {
		return skills.SkillVersion{}, skills.Promotion{}, err
	}
	if err := validatePromotionText(reason); err != nil {
		return skills.SkillVersion{}, skills.Promotion{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "skill publish reason is invalid", err)
	}
	var result skills.SkillVersion
	var promotion skills.Promotion
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadSkillVersion(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		result, promotion, err = skills.Publish(current, actorID, reason, s.store.clock.Now())
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "skill version cannot be published", err)
		}
		var publishedID sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT id FROM skill_versions WHERE workspace_id=? AND skill_id=? AND state='published' AND id<>?`, workspace, current.SkillID, id).Scan(&publishedID); err != nil && err != sql.ErrNoRows {
			return err
		}
		if publishedID.Valid {
			old, err := loadSkillVersion(ctx, tx, workspace, skills.SkillVersionID(publishedID.String))
			if err != nil {
				return err
			}
			deprecated, oldPromotion, err := skills.Deprecate(old, actorID, reason, s.store.clock.Now())
			if err != nil {
				return platformerrors.Wrap(platformerrors.CodeConflict, "published skill version cannot be deprecated", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE skill_versions SET state=? WHERE workspace_id=? AND id=? AND state='published'`, deprecated.State, workspace, old.ID); err != nil {
				return mapConstraint(err)
			}
			if err := s.insertPromotion(ctx, tx, &oldPromotion); err != nil {
				return err
			}
		}
		updated, err := tx.ExecContext(ctx, `UPDATE skill_versions SET state=? WHERE workspace_id=? AND id=? AND state='validated' AND trust_state='approved'`, result.State, workspace, id)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(updated, "skill version"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET state='published',updated_at=? WHERE workspace_id=? AND id=?`, s.store.clock.Now().UTC().Format(time.RFC3339Nano), workspace, current.SkillID); err != nil {
			return mapConstraint(err)
		}
		if err := s.insertPromotion(ctx, tx, &promotion); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "skill_version", string(id), "skill.version_published", "operator", actorID)
	})
	return result, promotion, err
}

func (s *SkillService) DeprecateVersion(ctx context.Context, workspace organizations.WorkspaceID, id skills.SkillVersionID, actorID, reason string) (skills.SkillVersion, skills.Promotion, error) {
	if err := validateSkillServiceInput(ctx, s, workspace, "operator", actorID); err != nil {
		return skills.SkillVersion{}, skills.Promotion{}, err
	}
	if err := validatePromotionText(reason); err != nil {
		return skills.SkillVersion{}, skills.Promotion{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "skill deprecation reason is invalid", err)
	}
	var result skills.SkillVersion
	var promotion skills.Promotion
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		current, err := loadSkillVersion(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		result, promotion, err = skills.Deprecate(current, actorID, reason, s.store.clock.Now())
		if err != nil {
			return platformerrors.Wrap(platformerrors.CodeConflict, "skill version cannot be deprecated", err)
		}
		updated, err := tx.ExecContext(ctx, `UPDATE skill_versions SET state=? WHERE workspace_id=? AND id=? AND state='published'`, result.State, workspace, id)
		if err != nil {
			return mapConstraint(err)
		}
		if err := RequireAffected(updated, "skill version"); err != nil {
			return err
		}
		if err := s.insertPromotion(ctx, tx, &promotion); err != nil {
			return err
		}
		return s.store.recordMutation(ctx, tx, string(workspace), "skill_version", string(id), "skill.version_deprecated", "operator", actorID)
	})
	return result, promotion, err
}

func (s *SkillService) RecordBrainKnowledge(ctx context.Context, knowledge skills.BrainKnowledge, actorType, actorID string) (skills.BrainKnowledge, error) {
	if err := validateSkillService(ctx, s, knowledge.Workspace, actorType, actorID); err != nil {
		return skills.BrainKnowledge{}, err
	}
	if strings.TrimSpace(knowledge.ID) == "" {
		id, err := s.store.ids.NewID()
		if err != nil {
			return skills.BrainKnowledge{}, platformerrors.Wrap(platformerrors.CodeInternal, "generate shared-brain knowledge ID", err)
		}
		knowledge.ID = id
	}
	if knowledge.State == "" {
		knowledge.State = skills.Draft
	}
	if len(knowledge.Key) > 256 || redaction.RedactString(knowledge.Key) != knowledge.Key {
		return skills.BrainKnowledge{}, platformerrors.New(platformerrors.CodeInvalidInput, "shared-brain knowledge key is invalid or sensitive")
	}
	if err := knowledge.Validate(); err != nil {
		return skills.BrainKnowledge{}, platformerrors.Wrap(platformerrors.CodeInvalidInput, "shared-brain knowledge is invalid", err)
	}
	if !safeSkillJSON(knowledge.KnowledgeJSON) {
		return skills.BrainKnowledge{}, platformerrors.New(platformerrors.CodeInvalidInput, "shared-brain knowledge is sensitive")
	}
	err := WithTx(ctx, s.store.db, func(tx *sql.Tx) error {
		if knowledge.SourceSkillVersion != "" {
			var trust, state string
			if err := tx.QueryRowContext(ctx, `SELECT trust_state,state FROM skill_versions WHERE workspace_id=? AND id=?`, knowledge.Workspace, knowledge.SourceSkillVersion).Scan(&trust, &state); err == sql.ErrNoRows {
				return platformerrors.New(platformerrors.CodeNotFound, "source skill version not found")
			} else if err != nil {
				return err
			}
			if skills.TrustState(trust) != skills.TrustApproved || (skills.State(state) != skills.Validated && skills.State(state) != skills.Published && skills.State(state) != skills.Deprecated) {
				return platformerrors.New(platformerrors.CodeConflict, "shared-brain knowledge requires an approved skill source")
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO shared_brain_knowledge (id,workspace_id,knowledge_key,version,state,knowledge_json,source_skill_version_id,created_at) VALUES (?,?,?,?,?,?,?,?)`, knowledge.ID, knowledge.Workspace, knowledge.Key, knowledge.Version, knowledge.State, knowledge.KnowledgeJSON, nullableString(string(knowledge.SourceSkillVersion)), s.store.clock.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return mapConstraint(err)
		}
		return s.store.recordMutation(ctx, tx, string(knowledge.Workspace), "shared_brain_knowledge", knowledge.ID, "skill.brain_knowledge_recorded", actorType, actorID)
	})
	return knowledge, err
}

func validateSkillService(ctx context.Context, service *SkillService, workspace organizations.WorkspaceID, actorType, actorID string) error {
	if ctx == nil || service == nil || service.store == nil || service.store.db == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "context and SQLite service are required")
	}
	if err := validateWorkspace(string(workspace)); err != nil {
		return err
	}
	if strings.TrimSpace(actorType) == "" || strings.TrimSpace(actorID) == "" {
		return platformerrors.New(platformerrors.CodeInvalidInput, "skill actor fields are required")
	}
	return nil
}

func validateSkillServiceInput(ctx context.Context, service *SkillService, workspace organizations.WorkspaceID, actorType, actorID string) error {
	return validateSkillService(ctx, service, workspace, actorType, actorID)
}

func validatePromotionText(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 512 || redaction.RedactString(value) != value {
		return fmt.Errorf("promotion reason is empty, oversized, or sensitive")
	}
	return nil
}

func (s *SkillService) insertPromotion(ctx context.Context, tx *sql.Tx, promotion *skills.Promotion) error {
	if promotion == nil {
		return platformerrors.New(platformerrors.CodeInvalidInput, "skill promotion is required")
	}
	id, err := s.store.ids.NewID()
	if err != nil {
		return platformerrors.Wrap(platformerrors.CodeInternal, "generate skill promotion ID", err)
	}
	promotion.ID = id
	_, err = tx.ExecContext(ctx, `INSERT INTO skill_promotions (id,workspace_id,skill_version_id,from_state,to_state,actor_id,reason,occurred_at) VALUES (?,?,?,?,?,?,?,?)`, promotion.ID, promotion.Workspace, promotion.SkillVersion, promotion.From, promotion.To, promotion.ActorID, promotion.Reason, promotion.OccurredAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return mapConstraint(err)
	}
	return nil
}

func loadSkillVersion(ctx context.Context, queryer sqlContextQuerier, workspace organizations.WorkspaceID, id skills.SkillVersionID) (skills.SkillVersion, error) {
	var result skills.SkillVersion
	var state, definition, capabilities, trust, source, created, manifest, fixtures, risk, retry, reviewer, reviewed, rollback sql.NullString
	var version int
	err := queryer.QueryRowContext(ctx, `SELECT id,workspace_id,skill_id,version,state,definition_json,capabilities_json,trust_state,source_recording_session_id,created_at,manifest_json,fixtures_json,risk_class,retry_class,reviewer_id,reviewed_at,rollback_of FROM skill_versions WHERE workspace_id=? AND id=?`, workspace, id).Scan(&result.ID, &result.Workspace, &result.SkillID, &version, &state, &definition, &capabilities, &trust, &source, &created, &manifest, &fixtures, &risk, &retry, &reviewer, &reviewed, &rollback)
	if err == sql.ErrNoRows {
		return result, platformerrors.New(platformerrors.CodeNotFound, "skill version not found")
	}
	if err != nil {
		return result, classifyContext(err)
	}
	result.Version = version
	result.State = skills.State(state.String)
	result.Trust = skills.TrustState(trust.String)
	result.SourceRecordingSession = recordings.RecordingSessionID(source.String)
	result.CreatedAt = parseTimeOrZero(created.String)
	result.ReviewerID = reviewer.String
	result.ReviewedAt = parseNullableTime(reviewed)
	result.RollbackOf = skills.SkillVersionID(rollback.String)
	var storedDefinition struct {
		Version int              `json:"version"`
		Steps   []workflows.Step `json:"steps"`
	}
	if err := json.Unmarshal([]byte(definition.String), &storedDefinition); err != nil {
		return result, platformerrors.Wrap(platformerrors.CodeInternal, "decode skill definition", err)
	}
	result.Steps = storedDefinition.Steps
	if err := json.Unmarshal([]byte(manifest.String), &result.Manifest); err != nil {
		return result, platformerrors.Wrap(platformerrors.CodeInternal, "decode skill manifest", err)
	}
	if err := json.Unmarshal([]byte(capabilities.String), &result.Manifest.RequestedCapabilities); err != nil {
		return result, platformerrors.Wrap(platformerrors.CodeInternal, "decode skill capabilities", err)
	}
	if err := json.Unmarshal([]byte(fixtures.String), &result.Manifest.Fixtures); err != nil {
		return result, platformerrors.Wrap(platformerrors.CodeInternal, "decode skill fixtures", err)
	}
	result.Manifest.Risk = action.RiskClass(risk.String)
	result.Manifest.Retry = action.RetryClass(retry.String)
	return result, nil
}

func jsonArray(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if string(encoded) == "null" {
		return "[]", nil
	}
	return string(encoded), nil
}

func safeSkillJSON(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "credential") || strings.Contains(lower, "private_key") || redaction.RedactString(value) != value {
		return false
	}
	return true
}
