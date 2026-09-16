package uiautomator_test

import (
	"context"
	"testing"

	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/edge/uiautomator"
)

// The foreground package is a fact a launch postcondition is evaluated against,
// so the capture either names the package the focused node reports or names
// none. It never guesses: an empty value means "this capture cannot say which
// package is in the foreground", and the postcondition that names one is left
// unverifiable rather than failed.

const focusedChildHierarchy = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="com.example.host" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" focused="false" bounds="[0,0][1080,2340]">
    <node index="0" text="Ready" resource-id="com.example.target:id/title" class="android.widget.TextView" package="com.example.target" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" focused="true" bounds="[40,120][1040,200]" />
  </node>
</hierarchy>
`

const noFocusedNodeHierarchy = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="com.example.host" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" bounds="[0,0][1080,2340]">
    <node index="0" text="Ready" resource-id="com.example.host:id/title" class="android.widget.TextView" package="com.example.host" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" bounds="[40,120][1040,200]" />
  </node>
</hierarchy>
`

const twoFocusedPackagesHierarchy = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="com.example.host" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" focused="true" bounds="[0,0][1080,2340]">
    <node index="0" text="Overlay" resource-id="" class="android.widget.TextView" package="com.example.overlay" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" focused="true" bounds="[40,120][1040,200]" />
  </node>
</hierarchy>
`

const twoFocusedNodesOnePackageHierarchy = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="com.example.target" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" focused="true" bounds="[0,0][1080,2340]">
    <node index="0" text="Ready" resource-id="com.example.target:id/title" class="android.widget.TextView" package="com.example.target" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" focused="true" bounds="[40,120][1040,200]" />
  </node>
</hierarchy>
`

const focusedPackageNotAPackageNameHierarchy = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="com.example.host" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" focused="true" bounds="[0,0][1080,2340]">
    <node index="0" text="Ready" resource-id="" class="android.widget.TextView" package="com.example target" content-desc="" checkable="false" enabled="true" clickable="false" scrollable="false" long-clickable="false" password="false" focused="true" bounds="[40,120][1040,200]" />
  </node>
</hierarchy>
`

func captureHierarchy(t *testing.T, hierarchy string) uiautomator.HierarchyCapture {
	t.Helper()
	adapter, runner := newHarness(t)
	runner.RespondStdout(serialArgs(adb.UIAutomatorDumpStdoutArgv()...), hierarchy)
	capture, err := adapter.Capture(context.Background(), testSerial)
	if err != nil {
		t.Fatalf("Capture() = %v", err)
	}
	return capture
}

// TestCaptureCarriesTheFocusedNodesPackage: the fact exists in the dump's own
// attributes, so the capture carries it instead of leaving every launch
// postcondition unverifiable.
func TestCaptureCarriesTheFocusedNodesPackage(t *testing.T) {
	capture := captureHierarchy(t, focusedChildHierarchy)
	if capture.ForegroundPackage != "com.example.target" {
		t.Fatalf("ForegroundPackage = %q, want the focused node's package %q", capture.ForegroundPackage, "com.example.target")
	}
	if !capture.Complete() {
		t.Fatalf("capture = %+v, want a complete observation", capture)
	}
}

// TestCaptureInventsNothingWhenNoNodeIsFocused: the hierarchy names a package on
// every node, and none of them is the foreground one. An empty result is the
// honest one; the first node's package is not a substitute.
func TestCaptureInventsNothingWhenNoNodeIsFocused(t *testing.T) {
	capture := captureHierarchy(t, noFocusedNodeHierarchy)
	if capture.ForegroundPackage != "" {
		t.Fatalf("ForegroundPackage = %q, want it left unobserved rather than taken from a node that is not focused", capture.ForegroundPackage)
	}
}

// TestCaptureRefusesToChooseBetweenDisagreeingFocusedNodes: two nodes claim
// focus and name different packages, so the capture cannot say which is in the
// foreground. Choosing either would be a guess a postcondition would then be
// evaluated against.
func TestCaptureRefusesToChooseBetweenDisagreeingFocusedNodes(t *testing.T) {
	capture := captureHierarchy(t, twoFocusedPackagesHierarchy)
	if capture.ForegroundPackage != "" {
		t.Fatalf("ForegroundPackage = %q, want no package when focused nodes disagree", capture.ForegroundPackage)
	}
}

// TestCaptureAcceptsAgreeingFocusedNodes: a focused container and a focused
// child of the same application determine the foreground package rather than
// making it ambiguous.
func TestCaptureAcceptsAgreeingFocusedNodes(t *testing.T) {
	capture := captureHierarchy(t, twoFocusedNodesOnePackageHierarchy)
	if capture.ForegroundPackage != "com.example.target" {
		t.Fatalf("ForegroundPackage = %q, want %q from two focused nodes that agree", capture.ForegroundPackage, "com.example.target")
	}
}

// TestCaptureRefusesAFocusedPackageThatIsNotAPackageName: the dump is device
// output, so the attribute is untrusted input. A value that is not a package
// name is not carried into an observation that a postcondition is compared
// against.
func TestCaptureRefusesAFocusedPackageThatIsNotAPackageName(t *testing.T) {
	capture := captureHierarchy(t, focusedPackageNotAPackageNameHierarchy)
	if capture.ForegroundPackage != "" {
		t.Fatalf("ForegroundPackage = %q, want a value that is not a package name refused rather than carried", capture.ForegroundPackage)
	}
}
