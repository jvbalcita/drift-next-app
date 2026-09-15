// Command drift is the single local operator entry point for Drift Next.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"drift.local/drift-next/internal/runtime"
)

func main() {
	dataDir := runtime.DataDir()
	cfg, err := runtime.LoadOrCreate(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
		os.Exit(1)
	}
	supervisor := runtime.NewSupervisor(cfg, dataDir)
	reader := bufio.NewReader(os.Stdin)
	for {
		printMenu()
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(choice)
		ctx := context.Background()
		var actionErr error
		switch choice {
		case "1":
			actionErr = supervisor.Setup(ctx)
			if actionErr == nil {
				fmt.Println("Environment ready.")
			}
		case "2":
			actionErr = supervisor.StartAll(ctx, true)
		case "3":
			actionErr = supervisor.StopAll(ctx)
		case "4":
			_ = supervisor.StopAll(ctx)
			actionErr = supervisor.StartAll(ctx, true)
		case "5":
			actionErr = supervisor.RunChecks(ctx)
		case "6":
			actionErr = supervisor.BuildAll(ctx)
		case "7":
			actionErr = supervisor.RunRealDeviceTests(ctx)
		case "8":
			for _, status := range supervisor.Status() {
				fmt.Printf("%-24s %-10s %s\n", status.Name, status.State, status.Detail)
			}
		case "9":
			for _, line := range supervisor.Logs() {
				fmt.Println(line)
			}
		case "10", "q", "Q":
			_ = supervisor.StopAll(ctx)
			return
		case "11":
			actionErr = supervisor.StartComponent(ctx, "Control Plane")
		case "12":
			actionErr = supervisor.StartComponent(ctx, "Device Service")
		case "13":
			actionErr = supervisor.StartComponent(ctx, "Desktop Application")
		case "14":
			actionErr = supervisor.StopComponent("Control Plane")
		case "15":
			actionErr = supervisor.StopComponent("Device Service")
		case "16":
			actionErr = supervisor.StopComponent("Desktop Application")
		default:
			fmt.Println("Choose a listed command.")
		}
		if actionErr != nil {
			fmt.Printf("Error: %v\n", actionErr)
		}
		if choice != "8" && choice != "9" {
			fmt.Println()
		}
	}
}

func printMenu() {
	fmt.Println("Drift Next Runtime")
	fmt.Println("1) Setup Environment")
	fmt.Println("2) Start All")
	fmt.Println("3) Stop All")
	fmt.Println("4) Restart All")
	fmt.Println("5) Run All Checks")
	fmt.Println("6) Build All")
	fmt.Println("7) Run Real-Device Tests")
	fmt.Println("8) Show Status")
	fmt.Println("9) Show Logs")
	fmt.Println("10) Exit")
	fmt.Println("11) Start Control Plane")
	fmt.Println("12) Start Device Service")
	fmt.Println("13) Start Desktop Application")
	fmt.Println("14) Stop Control Plane")
	fmt.Println("15) Stop Device Service")
	fmt.Println("16) Stop Desktop Application")
	fmt.Print("> ")
}
