// Package adapter defines the narrow edge execution boundary. Implementations
// receive typed, already-authorized intents and never receive raw shell or
// transport commands.
package adapter

import (
	"context"
	"errors"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/domain"
)

type TargetNode struct {
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

type Observation struct {
	Token           string
	PackageName     string
	ActivityName    string
	CoordinateSpace string
	Nodes           []TargetNode
	OCR             []OCRToken
	ScreenshotHash  string
	Partial         bool
	FailureClass    domain.FailureClass
}

type Execution struct {
	Outcome          action.Outcome
	Postcondition    action.PostconditionState
	FailureClass     domain.FailureClass
	Dispatched       bool
	TransportLost    bool
	ObservationToken string
}

// ExecutionError carries the only uncertainty an adapter may add: whether a
// transport failure happened before or after dispatch. The caller must keep a
// post-dispatch failure indeterminate.
type ExecutionError struct {
	Cause        error
	Dispatched   bool
	FailureClass domain.FailureClass
}

func (e *ExecutionError) Error() string {
	if e == nil || e.Cause == nil {
		return "edge execution failed"
	}
	return e.Cause.Error()
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type Adapter interface {
	Capabilities() []action.Capability
	Observe(context.Context) (Observation, error)
	Execute(context.Context, action.Intent) (Execution, error)
	Cleanup(context.Context, action.Intent) error
}

func IsExecutionError(err error) (*ExecutionError, bool) {
	var executionErr *ExecutionError
	return executionErr, errors.As(err, &executionErr)
}
