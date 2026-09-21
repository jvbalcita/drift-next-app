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
	ContextFingerprint string
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
	AppVersion      string
	CoordinateSpace string
	DisplayWidth    int
	DisplayHeight   int
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
//
// ObservationToken is the fresh reading the boundary took AFTER the attempt,
// when it took one. An attempt that did not reach the device still has to be
// completed, and the kernel requires a completion to name a reading taken after
// the action - so a boundary that refuses an input takes the reading its
// completion will be submitted against and reports it here. An EMPTY token means
// no reading was taken (the device could not be read), and the caller must not
// complete the attempt at all: a completion carrying the token the attempt was
// dispatched with, or none, is refused as stale, and that refusal would replace
// the reason the input failed with a sentence about an observation nobody took.
type ExecutionError struct {
	Cause            error
	Dispatched       bool
	FailureClass     domain.FailureClass
	ObservationToken string
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
