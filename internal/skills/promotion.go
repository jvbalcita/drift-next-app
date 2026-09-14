package skills

import (
	"fmt"
	"strings"
	"time"
)

// Review marks a compiled draft as validated only after a named human review.
// The resulting version is still immutable; later changes require a new
// version and a new source recording.
func Review(version SkillVersion, reviewer, reason string, at time.Time) (SkillVersion, Promotion, error) {
	var promotion Promotion
	if err := version.Validate(); err != nil {
		return version, promotion, err
	}
	if version.State != Draft || version.Trust != TrustUnreviewed {
		return version, promotion, fmt.Errorf("only an unreviewed draft can be reviewed")
	}
	if strings.TrimSpace(reviewer) == "" || strings.TrimSpace(reason) == "" || at.IsZero() {
		return version, promotion, fmt.Errorf("reviewer, reason, and review time are required")
	}
	at = at.UTC()
	version.State = Validated
	version.Trust = TrustApproved
	version.ReviewedAt = &at
	version.ReviewerID = reviewer
	promotion = Promotion{Workspace: version.Workspace, SkillVersion: version.ID, From: Draft, To: Validated, ActorID: reviewer, Reason: reason, OccurredAt: at}
	return version, promotion, nil
}

func Publish(version SkillVersion, actorID, reason string, at time.Time) (SkillVersion, Promotion, error) {
	var promotion Promotion
	if err := version.Validate(); err != nil {
		return version, promotion, err
	}
	if version.State != Validated || version.Trust != TrustApproved {
		return version, promotion, fmt.Errorf("only an approved validated skill can be published")
	}
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(reason) == "" || at.IsZero() {
		return version, promotion, fmt.Errorf("publisher, reason, and publish time are required")
	}
	version.State = Published
	promotion = Promotion{Workspace: version.Workspace, SkillVersion: version.ID, From: Validated, To: Published, ActorID: actorID, Reason: reason, OccurredAt: at.UTC()}
	return version, promotion, nil
}

func Deprecate(version SkillVersion, actorID, reason string, at time.Time) (SkillVersion, Promotion, error) {
	if err := version.Validate(); err != nil {
		return version, Promotion{}, err
	}
	if version.State != Published || strings.TrimSpace(actorID) == "" || strings.TrimSpace(reason) == "" || at.IsZero() {
		return version, Promotion{}, fmt.Errorf("only a published skill can be deprecated")
	}
	version.State = Deprecated
	return version, Promotion{Workspace: version.Workspace, SkillVersion: version.ID, From: Published, To: Deprecated, ActorID: actorID, Reason: reason, OccurredAt: at.UTC()}, nil
}

// RollbackTarget describes an explicit version to restore. Rollback never
// edits the old version; persistence records one deprecation and one publish
// promotion, retaining the full version history.
func RollbackTarget(current, previous SkillVersion) error {
	if err := current.Validate(); err != nil {
		return err
	}
	if err := previous.Validate(); err != nil {
		return err
	}
	if current.Workspace != previous.Workspace || current.SkillID != previous.SkillID || current.ID == previous.ID || previous.State == Retired || previous.Trust != TrustApproved {
		return fmt.Errorf("rollback versions are incompatible")
	}
	if current.State != Published && current.State != Deprecated {
		return fmt.Errorf("only published or deprecated versions can be rolled back")
	}
	return nil
}
