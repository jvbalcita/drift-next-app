package main

import (
	"context"

	"drift.local/drift-next/internal/runtime"
)

// ops narrows the supervisor to exactly the behaviour the console drives. It
// keeps the input loop testable without a live supervisor and leaves
// internal/runtime — including its exported API — untouched.
type ops struct {
	status          func() []runtime.ComponentStatus
	logs            func() []string
	refresh         func(context.Context) error
	setEventSink    func(func(string))
	setup           func(context.Context) error
	startAll        func(context.Context) error
	stopAll         func(context.Context) error
	runChecks       func(context.Context) error
	buildAll        func(context.Context) error
	realDeviceTests func(context.Context) error
	startComponent  func(context.Context, string) error
	stopComponent   func(string) error
	// externalComponents is what the operator is offered a decision about, and
	// adopt and terminate are the two decisions. They exist separately from the
	// rest because the choice must be visible before it is made.
	externalComponents func() []runtime.ExternalComponent
	adopt              func(string) error
	terminate          func(context.Context, string) error
}

func supervisorOps(supervisor *runtime.Supervisor) ops {
	return ops{
		status:             supervisor.Status,
		logs:               supervisor.Logs,
		refresh:            func(ctx context.Context) error { supervisor.RefreshStatus(ctx); return nil },
		setEventSink:       supervisor.SetEventSink,
		setup:              supervisor.Setup,
		startAll:           func(ctx context.Context) error { return supervisor.StartAll(ctx, true) },
		stopAll:            supervisor.StopAll,
		runChecks:          supervisor.RunChecks,
		buildAll:           supervisor.BuildAll,
		realDeviceTests:    supervisor.RunRealDeviceTests,
		startComponent:     supervisor.StartComponent,
		stopComponent:      supervisor.StopComponent,
		externalComponents: supervisor.ExternalComponents,
		adopt:              supervisor.Adopt,
		terminate:          supervisor.Terminate,
	}
}
