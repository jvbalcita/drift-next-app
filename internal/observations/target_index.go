package observations

import (
	"errors"
	"fmt"
	"strings"

	"drift.local/drift-next/internal/platform/redaction"
)

var (
	ErrAmbiguousTarget     = errors.New("target is ambiguous")
	ErrTargetNotFound      = errors.New("target was not found")
	ErrTargetNotActionable = errors.New("target is not actionable")
	ErrStaleObservation    = errors.New("target observation is stale")
	ErrWrongApplication    = errors.New("target belongs to another application")
	ErrPartialObservation  = errors.New("target observation is partial")
	ErrShiftedTarget       = errors.New("target bounds shifted")
)

type TargetSource string

const (
	SourceXML           TargetSource = "xml"
	SourceAccessibility TargetSource = "accessibility"
	SourceOCR           TargetSource = "ocr"
)

type TargetNode struct {
	Source             TargetSource
	ResourceID         string
	AccessibilityLabel string
	StableText         string
	ClassName          string
	Bounds             [4]int
	Actionable         bool
	Enabled            bool
	Editable           bool
}

type OCRToken struct {
	Text       string
	Confidence float64
	Bounds     [4]int
}

type TargetQuery struct {
	ResourceID         string
	AccessibilityLabel string
	StableText         string
	ExpectedBounds     [4]int
	HasExpectedBounds  bool
}

type TargetCandidate struct {
	Source             TargetSource
	ResourceID         string
	AccessibilityLabel string
	StableText         string
	ClassName          string
	Bounds             [4]int
	Actionable         bool
	Enabled            bool
	Editable           bool
	Confidence         float64
}

type TargetIndex struct {
	ObservationToken string
	PackageName      string
	ActivityName     string
	CaptureStatus    CaptureStatus
	Candidates       []TargetCandidate
}

func BuildTargetIndex(observationToken string, nodes []TargetNode, ocr []OCRToken) (TargetIndex, error) {
	if strings.TrimSpace(observationToken) == "" || len(observationToken) > 256 {
		return TargetIndex{}, fmt.Errorf("observation token is required and bounded")
	}
	return buildTargetIndex(TargetIndex{ObservationToken: observationToken, CaptureStatus: CaptureComplete}, nodes, ocr)
}

// BuildTargetIndexForObservation binds semantic candidates to the application
// identity and capture state that produced them. Callers replaying an action
// should use ResolveAt rather than resolving against an unqualified index.
func BuildTargetIndexForObservation(observation ObservationSnapshot, nodes []TargetNode, ocr []OCRToken) (TargetIndex, error) {
	if err := observation.Validate(); err != nil {
		return TargetIndex{}, fmt.Errorf("observation cannot index targets: %w", err)
	}
	return buildTargetIndex(TargetIndex{
		ObservationToken: observation.FreshnessToken,
		PackageName:      observation.PackageName,
		ActivityName:     observation.ActivityName,
		CaptureStatus:    observation.CaptureStatus,
	}, nodes, ocr)
}

func buildTargetIndex(index TargetIndex, nodes []TargetNode, ocr []OCRToken) (TargetIndex, error) {
	index.Candidates = make([]TargetCandidate, 0, len(nodes)+len(ocr))
	for _, node := range nodes {
		source := node.Source
		if source == "" {
			source = SourceAccessibility
		}
		if (source != SourceXML && source != SourceAccessibility) || !validBounds(node.Bounds) || containsSensitive(node.ResourceID, node.AccessibilityLabel, node.StableText, node.ClassName) || !boundedTargetFields(node.ResourceID, node.AccessibilityLabel, node.StableText, node.ClassName) {
			return TargetIndex{}, fmt.Errorf("semantic target contains invalid or sensitive metadata")
		}
		index.Candidates = append(index.Candidates, TargetCandidate{
			Source:             source,
			ResourceID:         node.ResourceID,
			AccessibilityLabel: node.AccessibilityLabel,
			StableText:         node.StableText,
			ClassName:          node.ClassName,
			Bounds:             node.Bounds,
			Actionable:         node.Actionable,
			Enabled:            node.Enabled,
			Editable:           node.Editable,
			Confidence:         1,
		})
	}
	for _, token := range ocr {
		if strings.TrimSpace(token.Text) == "" || len(token.Text) > 512 || token.Confidence < 0 || token.Confidence > 1 || !validBounds(token.Bounds) || containsSensitive(token.Text) {
			return TargetIndex{}, fmt.Errorf("OCR target contains invalid or sensitive metadata")
		}
		index.Candidates = append(index.Candidates, TargetCandidate{
			Source:     SourceOCR,
			StableText: token.Text,
			Bounds:     token.Bounds,
			Confidence: token.Confidence,
			Actionable: false,
			Enabled:    false,
		})
	}
	return index, nil
}

func (i TargetIndex) Resolve(query TargetQuery) (TargetCandidate, error) {
	return i.resolve(query)
}

// ResolveAt requires the caller to prove that the index still describes the
// current observation and application surface. Exact semantic matches are
// preferred; OCR remains evidence only and can never authorize an action.
func (i TargetIndex) ResolveAt(observationToken, packageName, activityName string, query TargetQuery) (TargetCandidate, error) {
	if strings.TrimSpace(observationToken) == "" || observationToken != i.ObservationToken {
		return TargetCandidate{}, ErrStaleObservation
	}
	if i.CaptureStatus != "" && i.CaptureStatus != CaptureComplete {
		return TargetCandidate{}, ErrPartialObservation
	}
	if (i.PackageName != "" && packageName != i.PackageName) || (i.ActivityName != "" && activityName != i.ActivityName) {
		return TargetCandidate{}, ErrWrongApplication
	}
	return i.resolve(query)
}

func (i TargetIndex) resolve(query TargetQuery) (TargetCandidate, error) {
	criteria := []struct {
		value string
		match func(TargetCandidate, string) bool
	}{
		{query.ResourceID, func(candidate TargetCandidate, value string) bool { return candidate.ResourceID == value }},
		{query.AccessibilityLabel, func(candidate TargetCandidate, value string) bool { return candidate.AccessibilityLabel == value }},
		{query.StableText, func(candidate TargetCandidate, value string) bool { return candidate.StableText == value }},
	}
	for _, criterion := range criteria {
		if strings.TrimSpace(criterion.value) == "" {
			continue
		}
		allMatches := make([]TargetCandidate, 0, 1)
		actionableMatches := make([]TargetCandidate, 0, 1)
		for _, candidate := range i.Candidates {
			if !isSemanticSource(candidate.Source) || !criterion.match(candidate, criterion.value) {
				continue
			}
			allMatches = append(allMatches, candidate)
			if candidate.Actionable && candidate.Enabled {
				actionableMatches = append(actionableMatches, candidate)
			}
		}
		if len(allMatches) == 0 {
			continue
		}
		if len(actionableMatches) == 0 {
			return TargetCandidate{}, ErrTargetNotActionable
		}
		if len(actionableMatches) > 1 {
			return TargetCandidate{}, ErrAmbiguousTarget
		}
		if query.HasExpectedBounds && actionableMatches[0].Bounds != query.ExpectedBounds {
			return TargetCandidate{}, ErrShiftedTarget
		}
		return actionableMatches[0], nil
	}
	return TargetCandidate{}, ErrTargetNotFound
}

func isSemanticSource(source TargetSource) bool {
	return source == SourceXML || source == SourceAccessibility
}

func validBounds(bounds [4]int) bool {
	if bounds == [4]int{} {
		return true
	}
	return bounds[0] >= 0 && bounds[1] >= 0 && bounds[2] >= bounds[0] && bounds[3] >= bounds[1]
}

func boundedTargetFields(values ...string) bool {
	for _, value := range values {
		if len(value) > 512 {
			return false
		}
	}
	return true
}

func containsSensitive(values ...string) bool {
	for _, value := range values {
		if redaction.RedactString(value) != value {
			return true
		}
	}
	return false
}
