// Command drift is the single local operator entry point for Drift Next.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"drift.local/drift-next/internal/runtime"
)

const (
	green  = "\033[32m"
	red    = "\033[31m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	dim    = "\033[2m"
	reset  = "\033[0m"
)

func main() {
	dataDir := runtime.DataDir()
	cfg, err := runtime.LoadOrCreate(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
		os.Exit(1)
	}
	supervisor := runtime.NewSupervisor(cfg, dataDir)
	supervisor.SetEventSink(func(event string) { fmt.Printf("  %s•%s %s\n", dim, reset, event) })
	supervisor.RefreshStatus(context.Background())
	reader := bufio.NewReader(os.Stdin)
	for {
		printDashboard(supervisor)
		fmt.Printf("%sSelect an action [1-16, q]:%s ", cyan, reset)
		choice, readErr := reader.ReadString('\n')
		if readErr != nil && len(choice) == 0 {
			_ = supervisor.StopAll(context.Background())
			return
		}
		choice = strings.TrimSpace(choice)
		if choice == "10" || strings.EqualFold(choice, "q") {
			runAction("Stop owned processes and exit", func(ctx context.Context) error { return supervisor.StopAll(ctx) })
			return
		}
		runChoice(supervisor, choice)
		fmt.Printf("\n%sPress Enter to return to the dashboard.%s", dim, reset)
		_, _ = reader.ReadString('\n')
	}
}

func runChoice(supervisor *runtime.Supervisor, choice string) {
	switch choice {
	case "1":
		runAction("Setup Environment", func(ctx context.Context) error { return supervisor.Setup(ctx) })
	case "2":
		runAction("Start All", func(ctx context.Context) error { return supervisor.StartAll(ctx, true) })
	case "3":
		runAction("Stop All", func(ctx context.Context) error { return supervisor.StopAll(ctx) })
	case "4":
		runAction("Restart All", func(ctx context.Context) error { _ = supervisor.StopAll(ctx); return supervisor.StartAll(ctx, true) })
	case "5":
		runAction("Run All Checks", func(ctx context.Context) error { return supervisor.RunChecks(ctx) })
	case "6":
		runAction("Build All", func(ctx context.Context) error { return supervisor.BuildAll(ctx) })
	case "7":
		runAction("Run Real-Device Tests", func(ctx context.Context) error { return supervisor.RunRealDeviceTests(ctx) })
	case "8":
		showStatus(supervisor)
	case "9":
		showLogs(supervisor)
	case "11":
		runAction("Start Control Plane", func(ctx context.Context) error { return supervisor.StartComponent(ctx, "Control Plane") })
	case "12":
		runAction("Start Device Service", func(ctx context.Context) error { return supervisor.StartComponent(ctx, "Device Service") })
	case "13":
		runAction("Start Desktop Application", func(ctx context.Context) error { return supervisor.StartComponent(ctx, "Desktop Application") })
	case "14":
		runAction("Stop Control Plane", func(context.Context) error { return supervisor.StopComponent("Control Plane") })
	case "15":
		runAction("Stop Device Service", func(context.Context) error { return supervisor.StopComponent("Device Service") })
	case "16":
		runAction("Stop Desktop Application", func(context.Context) error { return supervisor.StopComponent("Desktop Application") })
	default:
		fmt.Printf("%s✗ Unknown action. Choose one of the listed numbers.%s\n", red, reset)
	}
}

func runAction(label string, action func(context.Context) error) {
	started := time.Now()
	fmt.Printf("\n%s▶ %s%s\n  %sRunning…%s\n", cyan, label, reset, yellow, reset)
	if err := action(context.Background()); err != nil {
		fmt.Printf("%s✗ Failed%s after %s: %v\n", red, reset, time.Since(started).Round(time.Millisecond), err)
		return
	}
	fmt.Printf("%s✓ Completed%s in %s\n", green, reset, time.Since(started).Round(time.Millisecond))
}

func printDashboard(supervisor *runtime.Supervisor) {
	fmt.Printf("\n%s╭────────────────────────────────────────────╮%s\n%s│ Drift Next · Local Runtime                 │%s\n%s╰────────────────────────────────────────────╯%s\n", cyan, reset, cyan, reset, cyan, reset)
	for _, status := range supervisor.Status() {
		icon, color := "○", dim
		switch status.State {
		case "ready":
			icon, color = "●", green
		case "starting":
			icon, color = "◐", yellow
		case "failed":
			icon, color = "×", red
		}
		detail := status.Detail
		if detail == "" {
			detail = "Not started"
		}
		fmt.Printf("  %s%s%s %-24s %s%s%s\n", color, icon, reset, status.Name, dim, detail, reset)
	}
	fmt.Println("\n  1  Setup Environment       5  Run All Checks")
	fmt.Println("  2  Start All               6  Build All")
	fmt.Println("  3  Stop All                7  Run Real-Device Tests")
	fmt.Println("  4  Restart All             8  Show Status")
	fmt.Println("  9  Show Logs              10  Exit")
	fmt.Println("\n  Troubleshooting: 11 Start Control Plane · 12 Start Device Service · 13 Start Desktop Application")
	fmt.Println("                   14 Stop Control Plane  · 15 Stop Device Service  · 16 Stop Desktop Application")
}

func showStatus(supervisor *runtime.Supervisor) {
	fmt.Printf("\n%sComponent Status%s\n", cyan, reset)
	for _, status := range supervisor.Status() {
		fmt.Printf("  %-24s %-10s %s\n", status.Name, status.State, status.Detail)
	}
}

func showLogs(supervisor *runtime.Supervisor) {
	fmt.Printf("\n%sRecent Runtime Events%s\n", cyan, reset)
	logs := supervisor.Logs()
	if len(logs) == 0 {
		fmt.Printf("  %sNo events recorded yet.%s\n", dim, reset)
		return
	}
	for _, line := range logs {
		fmt.Printf("  %s\n", line)
	}
}
