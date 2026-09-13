// Package packages owns trust metadata for imported package manifests.
package packages

import "drift.local/drift-next/internal/domain"

type ManifestID string

type TrustState string

const (
	TrustUnreviewed TrustState = "unreviewed"
	TrustReviewed   TrustState = "reviewed"
	TrustApproved   TrustState = "approved"
	TrustRevoked    TrustState = "revoked"
)

func (s TrustState) Valid() bool {
	switch s {
	case TrustUnreviewed, TrustReviewed, TrustApproved, TrustRevoked:
		return true
	default:
		return false
	}
}

func CanTransition(from, to TrustState) bool {
	switch from {
	case TrustUnreviewed:
		return to == TrustReviewed || to == TrustRevoked
	case TrustReviewed:
		return to == TrustApproved || to == TrustRevoked
	case TrustApproved:
		return to == TrustRevoked
	case TrustRevoked:
		return false
	default:
		return false
	}
}

func Transition(from, to TrustState) error {
	if !CanTransition(from, to) {
		return domain.InvalidTransition("package_manifest", string(from), string(to))
	}
	return nil
}
