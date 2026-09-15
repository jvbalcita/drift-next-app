//go:build realdevice

package realdevice_test

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/lab"
	"drift.local/drift-next/internal/edge/registration"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/service"
	store "drift.local/drift-next/internal/store/sqlite"
)

const envRealDeviceSerial = "DRIFT_REALDEVICE_SERIAL"

func TestAttendedRealDeviceRegistrationAndObservation(t *testing.T) {
	serials := attachedDeviceSerials(t)
	if len(serials) == 0 {
		t.Skip("skipping real-device test: adb devices shows no device state")
	}
	serial := strings.TrimSpace(os.Getenv(envRealDeviceSerial))
	if len(serials) > 1 && serial == "" {
		t.Skipf("skipping real-device test: multiple attached devices; set %s to select one", envRealDeviceSerial)
	}
	if serial == "" {
		serial = serials[0]
	}
	if err := adb.ValidateSerial(serial); err != nil {
		t.Fatalf("%s is not a valid device serial: %v", envRealDeviceSerial, err)
	}
	matched := false
	for _, candidate := range serials {
		if candidate == serial {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("%s=%q is not among attached devices", envRealDeviceSerial, serial)
	}

	if !lab.LabModeRequested(os.LookupEnv) {
		t.Log("enumerated serial present; lab mode unset so registration/observation APIs were not called")
		return
	}
	labToken := strings.TrimSpace(os.Getenv(lab.EnvLabToken))
	if labToken == "" {
		t.Skipf("lab mode is set but %s is empty; refusing to hit mutating APIs", lab.EnvLabToken)
	}

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "drift.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspace := organizations.Workspace{ID: "workspace-lab-local", Name: "Local Lab", State: organizations.WorkspaceActive}
	if err := store.NewWorkspaceService(db).Create(context.Background(), workspace, "operator", "op-1"); err != nil {
		t.Fatal(err)
	}
	labService, err := lab.NewServiceFromEnv(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	registrationService := registration.NewService(registration.Config{
		MaxRegisteredDevices: 0,
		Probe: registration.LabStatusProbe{
			Source:       labService,
			AllowedPorts: []uint16{5555},
		},
	})
	server := httptest.NewServer(service.NewHTTPServer("realdevice", "127.0.0.1:0",
		service.LabAdapterRoute(labService, labToken),
		service.LabRegistrationRouteWithStore(registrationService, db, labToken, []uint16{5555}),
	).Handler)
	t.Cleanup(server.Close)

	client := driftv1connect.NewLabRegistrationServiceClient(http.DefaultClient, server.URL, connectrpc.WithInterceptors(labTokenInterceptor(labToken)))
	status, err := client.GetLabRegistrationStatus(context.Background(), connectrpc.NewRequest(&driftv1.GetLabRegistrationStatusRequest{
		Workspace: &driftv1.WorkspaceRef{WorkspaceId: string(workspace.ID)},
		Context:   &driftv1.RequestContext{RequestId: "realdevice-status-1"},
		Serial:    serial,
	}))
	if err != nil {
		t.Fatalf("GetLabRegistrationStatus: %v", err)
	}
	t.Logf("registration status received for selected serial (state present=%t)", status.Msg != nil)

	adapter := driftv1connect.NewLabAdapterServiceClient(http.DefaultClient, server.URL, connectrpc.WithInterceptors(labTokenInterceptor(labToken)))
	discovered, err := adapter.DiscoverLabDevices(context.Background(), connectrpc.NewRequest(&driftv1.DiscoverLabDevicesRequest{
		Workspace:  &driftv1.WorkspaceRef{WorkspaceId: string(workspace.ID)},
		OperatorId: "op-1",
	}))
	if err != nil {
		t.Fatalf("DiscoverLabDevices: %v", err)
	}
	t.Logf("discovered %d lab candidates", len(discovered.Msg.GetStatus().GetDiscovered()))
}

func attachedDeviceSerials(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("adb", "devices").CombinedOutput()
	if err != nil {
		t.Skipf("skipping real-device test: adb devices failed: %v", err)
	}
	var serials []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" {
			serials = append(serials, fields[0])
		}
	}
	return serials
}

func labTokenInterceptor(token string) connectrpc.UnaryInterceptorFunc {
	return func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, request connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			request.Header().Set(service.LabTokenHeader, token)
			return next(ctx, request)
		}
	}
}
