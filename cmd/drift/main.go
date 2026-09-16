// Command drift is the single local operator entry point for Drift Next.
//
// The console renders one stable frame in place, refreshes observable runtime
// status on a timer and on demand, buffers asynchronous supervisor events into
// a bounded region of that frame, and gives the terminal back on every exit
// path — normal, error, signal, or panic. It uses raw ANSI only and never
// switches to the alternate screen or hides the cursor.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"drift.local/drift-next/internal/runtime"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "drift: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dataDir := runtime.DataDir()
	config, err := runtime.LoadOrCreate(dataDir)
	if err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	supervisor := runtime.NewSupervisor(config, dataDir)
	console := newConsole(ctx, supervisorOps(supervisor), os.Stdout, osTerminal{input: os.Stdin, output: os.Stdout}, os.Stdin, colorEnabled(os.Stdout, os.LookupEnv))
	if resize := resizeSignal(); resize != nil {
		resized := make(chan os.Signal, 1)
		signal.Notify(resized, resize)
		defer signal.Stop(resized)
		console.resize = resized
	}
	if err := console.run(); err != nil {
		return err
	}
	// Safe to print now: the restore path has already given the terminal back.
	fmt.Println("Drift Next runtime console closed.")
	return nil
}
