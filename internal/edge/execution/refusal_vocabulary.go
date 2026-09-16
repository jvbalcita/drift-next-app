// The refusal vocabulary, read from outside this package.
//
// The dispatch boundary's refusal reasons are deliberately finer grained than
// the kernel's error vocabulary, and until now they were only readable from
// inside this package: the definitions table is unexported. A transport boundary
// that has to answer a client with the *reason* rather than with one collapsed
// code needs to read the vocabulary, and — more importantly — a test needs to be
// able to prove that it maps the whole vocabulary rather than the part somebody
// remembered.
//
// This adds no behaviour: it is a read-only projection of the one table that
// already binds each reason to its stable code and failure classification.
package execution

import (
	"sort"

	"drift.local/drift-next/internal/domain"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

// RefusalDefinition is one entry of the refusal vocabulary in a form another
// boundary can read and map: the reason as the dispatch boundary reports it, the
// stable platform code and failure classification it is bound to, and the fixed
// operator-facing message for that reason.
type RefusalDefinition struct {
	Reason       RefusalReason
	Code         platformerrors.Code
	FailureClass domain.FailureClass
	Message      string
}

// RefusalDefinitions returns the whole refusal vocabulary, sorted by reason so a
// caller or a test sees a stable order. A reason added to the boundary appears
// here automatically, which is what makes a mapping over this vocabulary total:
// a boundary that does not handle a new reason fails its own test rather than
// reporting it as something generic.
func RefusalDefinitions() []RefusalDefinition {
	definitions := make([]RefusalDefinition, 0, len(refusalDefinitions))
	for reason, definition := range refusalDefinitions {
		definitions = append(definitions, RefusalDefinition{
			Reason:       reason,
			Code:         definition.code,
			FailureClass: definition.failure,
			Message:      definition.message,
		})
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Reason < definitions[j].Reason })
	return definitions
}
