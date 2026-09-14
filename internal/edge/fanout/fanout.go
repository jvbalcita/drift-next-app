// Package fanout models manual source-and-follower execution without copying
// source coordinates or control metadata. Each follower owns its own typed
// intent, actor, lease, observation, and result.
package fanout

import (
	"context"
	"sort"
	"strings"
	"sync"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/edge/runner"
	platformerrors "drift.local/drift-next/internal/platform/errors"
)

type FailurePolicy string

const (
	Continue FailurePolicy = "continue"
	Pause    FailurePolicy = "pause"
	StopAll  FailurePolicy = "stop_all"
)

type Target struct {
	DeviceID string
	Intent   action.Intent
	Runner   *runner.Runner
}

type Result struct {
	DeviceID string
	Action   action.Result
	Err      error
}

type Session struct {
	SourceDeviceID string
	FailurePolicy  FailurePolicy
	Targets        []Target
}

func (s Session) Run(ctx context.Context, actorType, actorID string) ([]Result, error) {
	if ctx == nil || strings.TrimSpace(s.SourceDeviceID) == "" || (s.FailurePolicy != Continue && s.FailurePolicy != Pause && s.FailurePolicy != StopAll) {
		return nil, platformerrors.New(platformerrors.CodeInvalidInput, "source, failure policy, and context are required")
	}
	seen := make(map[string]struct{}, len(s.Targets))
	for _, target := range s.Targets {
		if strings.TrimSpace(target.DeviceID) == "" || target.DeviceID == s.SourceDeviceID || target.Runner == nil || target.Intent.DeviceID != target.DeviceID {
			return nil, platformerrors.New(platformerrors.CodeInvalidInput, "followers must be distinct typed target actors")
		}
		if _, exists := seen[target.DeviceID]; exists {
			return nil, platformerrors.New(platformerrors.CodeConflict, "a follower device may appear only once")
		}
		seen[target.DeviceID] = struct{}{}
	}
	childContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan Result, len(s.Targets))
	var wait sync.WaitGroup
	for _, target := range s.Targets {
		target := target
		wait.Add(1)
		go func() {
			defer wait.Done()
			actionResult, err := target.Runner.Run(childContext, target.Intent, actorType, actorID)
			results <- Result{DeviceID: target.DeviceID, Action: actionResult, Err: err}
			if err != nil && s.FailurePolicy == StopAll {
				cancel()
			}
		}()
	}
	wait.Wait()
	close(results)
	ordered := make([]Result, 0, len(s.Targets))
	for result := range results {
		ordered = append(ordered, result)
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].DeviceID < ordered[right].DeviceID })
	return ordered, nil
}
