// Package uiautomator captures an Android view hierarchy through the native
// `uiautomator dump --compressed` path. It observes only: it issues no input,
// holds no lease, and never claims a complete hierarchy it could not fully
// parse within its bounds.
package uiautomator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/edge/adapter"
	"drift.local/drift-next/internal/edge/adb"
	"drift.local/drift-next/internal/platform/clock"
	"drift.local/drift-next/internal/platform/ids"
	"drift.local/drift-next/internal/platform/redaction"
)

const (
	// AdapterVersion identifies this observation contract.
	AdapterVersion = "p13.1.0"

	// Source names the observation producer recorded with every capture.
	Source = "uiautomator"

	// ProtocolVersion names the on-device dump format this adapter parses.
	ProtocolVersion = "dump-compressed-v1"

	// DefaultMaxNodes bounds retained hierarchy nodes.
	DefaultMaxNodes = 4000

	// DefaultMaxDepth bounds retained hierarchy depth.
	DefaultMaxDepth = 64

	// DefaultMaxXMLBytes bounds the dump payload parsed per capture.
	DefaultMaxXMLBytes = 4 << 20
)

var (
	// ErrCommandsRequired reports a missing allowlisted command runner.
	ErrCommandsRequired = errors.New("uiautomator command runner is required")

	// ErrDumpEmpty reports a dump that produced no hierarchy payload.
	ErrDumpEmpty = errors.New("uiautomator dump produced no hierarchy")

	// ErrDumpMalformed reports a payload that is not parsable hierarchy XML.
	ErrDumpMalformed = errors.New("uiautomator dump is not parsable hierarchy XML")
)

// CommandRunner is the narrow slice of the ADB adapter this package needs. It
// only accepts allowlisted argument arrays, so no shell surface reaches here.
type CommandRunner interface {
	ValidateSerial(serial string) error
	RunAllowlisted(ctx context.Context, serial string, args []string) (adb.Result, error)
}

// HierarchyCapture is one bounded observation. A capture is only complete when
// FailureClass is empty and neither Partial nor Truncated is set.
type HierarchyCapture struct {
	Serial          string
	CorrelationID   string
	CapturedAt      time.Time
	Source          string
	ProtocolVersion string
	AdapterVersion  string
	NodeCount       int
	MaxDepth        int
	Truncated       bool
	Partial         bool
	Sanitized       bool
	CleanupFailed   bool
	FreshnessToken  string
	FailureClass    domain.FailureClass
	Nodes           []adapter.TargetNode
	RawSize         int
	Latency         time.Duration
}

// Complete reports whether the capture may be treated as a full hierarchy.
func (c HierarchyCapture) Complete() bool {
	return c.FailureClass == "" && !c.Partial && !c.Truncated
}

// Adapter captures hierarchies for one ADB command boundary.
type Adapter struct {
	commands    CommandRunner
	clock       clock.Clock
	ids         ids.IDGenerator
	maxNodes    int
	maxDepth    int
	maxXMLBytes int
}

// Option configures an Adapter at construction time.
type Option func(*Adapter) error

// WithClock replaces the capture clock.
func WithClock(source clock.Clock) Option {
	return func(a *Adapter) error {
		if source == nil {
			return errors.New("uiautomator clock is required")
		}
		a.clock = source
		return nil
	}
}

// WithIDGenerator replaces the correlation-identifier source.
func WithIDGenerator(generator ids.IDGenerator) Option {
	return func(a *Adapter) error {
		if generator == nil {
			return errors.New("uiautomator ID generator is required")
		}
		a.ids = generator
		return nil
	}
}

// WithMaxNodes bounds retained nodes per capture.
func WithMaxNodes(limit int) Option {
	return func(a *Adapter) error {
		if limit <= 0 {
			return errors.New("uiautomator node limit must be positive")
		}
		a.maxNodes = limit
		return nil
	}
}

// WithMaxDepth bounds retained depth per capture.
func WithMaxDepth(limit int) Option {
	return func(a *Adapter) error {
		if limit <= 0 {
			return errors.New("uiautomator depth limit must be positive")
		}
		a.maxDepth = limit
		return nil
	}
}

// WithMaxXMLBytes bounds the parsed dump payload.
func WithMaxXMLBytes(limit int) Option {
	return func(a *Adapter) error {
		if limit <= 0 {
			return errors.New("uiautomator payload limit must be positive")
		}
		a.maxXMLBytes = limit
		return nil
	}
}

// New returns an adapter bound to an allowlisted command runner.
func New(commands CommandRunner, opts ...Option) (*Adapter, error) {
	if commands == nil {
		return nil, ErrCommandsRequired
	}
	instance := &Adapter{
		commands:    commands,
		clock:       clock.System{},
		ids:         ids.NewRandom(),
		maxNodes:    DefaultMaxNodes,
		maxDepth:    DefaultMaxDepth,
		maxXMLBytes: DefaultMaxXMLBytes,
	}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("uiautomator option is required")
		}
		if err := opt(instance); err != nil {
			return nil, err
		}
	}
	return instance, nil
}

// Capture observes the current hierarchy for serial.
//
// It first streams `exec-out uiautomator dump --compressed /dev/tty`, which
// creates no device-side file. If that path yields no hierarchy, it falls back
// to dumping into an adapter-owned temporary file, reading it with exec-out
// cat, and removing it. The temporary file is removed after every fallback
// attempt, including cancellation and failure paths.
//
// A bound reached by this adapter (nodes, depth, or payload size) returns a
// capture marked Truncated and Partial with FailureObservation and a nil
// error. A malformed, empty, or unreadable dump returns both a Partial capture
// and a classified error. A capture is never reported as complete unless
// Complete reports true.
func (a *Adapter) Capture(ctx context.Context, serial string) (HierarchyCapture, error) {
	if a == nil || a.commands == nil {
		return HierarchyCapture{}, ErrCommandsRequired
	}
	if err := a.commands.ValidateSerial(serial); err != nil {
		return HierarchyCapture{}, &adb.OperationError{
			Op:           "uiautomator-capture",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        err,
		}
	}
	correlationID, err := a.ids.NewID()
	if err != nil {
		return HierarchyCapture{}, &adb.OperationError{
			Op:           "uiautomator-capture",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        err,
		}
	}

	capture := HierarchyCapture{
		Serial:          serial,
		CorrelationID:   correlationID,
		CapturedAt:      a.clock.Now(),
		Source:          Source,
		ProtocolVersion: ProtocolVersion,
		AdapterVersion:  AdapterVersion,
		Sanitized:       true,
	}

	payload, latency, cleanupFailed, dumpErr := a.dump(ctx, serial, correlationID)
	capture.Latency = latency
	capture.CleanupFailed = cleanupFailed
	if dumpErr != nil {
		capture.Partial = true
		capture.FailureClass = failureClassFor(dumpErr)
		return capture, dumpErr
	}

	capture.RawSize = len(payload.raw)
	bounded := payload.raw
	boundApplied := payload.truncated
	if len(bounded) > a.maxXMLBytes {
		bounded = bounded[:a.maxXMLBytes]
		boundApplied = true
	}
	capture.FreshnessToken = "sha256:" + hashHex(bounded)

	parsed, parseErr := a.parse(bounded)
	capture.Nodes = parsed.nodes
	capture.NodeCount = len(parsed.nodes)
	capture.MaxDepth = parsed.maxDepth

	if parseErr != nil {
		capture.Partial = true
		capture.FailureClass = domain.FailureObservation
		if !boundApplied {
			return capture, &adb.OperationError{
				Op:           "uiautomator-capture",
				Serial:       serial,
				FailureClass: domain.FailureObservation,
				Detail:       adb.RedactOutput(bounded),
				Cause:        parseErr,
			}
		}
		capture.Truncated = true
		return capture, nil
	}
	if parsed.truncated || boundApplied {
		capture.Truncated = true
		capture.Partial = true
		capture.FailureClass = domain.FailureObservation
		return capture, nil
	}
	if capture.NodeCount == 0 {
		capture.Partial = true
		capture.FailureClass = domain.FailureObservation
		return capture, &adb.OperationError{
			Op:           "uiautomator-capture",
			Serial:       serial,
			FailureClass: domain.FailureObservation,
			Cause:        ErrDumpEmpty,
		}
	}
	if cleanupFailed {
		capture.FailureClass = domain.FailureCleanupFailed
	}
	return capture, nil
}

type dumpPayload struct {
	raw       []byte
	truncated bool
}

// dump runs the stdout strategy and falls back to the temporary-file strategy.
// The second return value is the accumulated invocation latency; the third
// reports whether device-side cleanup failed.
func (a *Adapter) dump(ctx context.Context, serial, correlationID string) (dumpPayload, time.Duration, bool, error) {
	var latency time.Duration

	streamed, streamErr := a.commands.RunAllowlisted(ctx, serial, adb.UIAutomatorDumpStdoutArgv())
	latency += streamed.Duration
	if streamErr == nil {
		if hierarchy, ok := extractHierarchy(streamed.Stdout); ok {
			return dumpPayload{raw: hierarchy, truncated: streamed.StdoutTruncated}, latency, false, nil
		}
	} else if isFatal(streamErr) {
		return dumpPayload{}, latency, false, streamErr
	}

	devicePath, pathErr := adb.DevicePath(correlationID)
	if pathErr != nil {
		return dumpPayload{}, latency, false, &adb.OperationError{
			Op:           "uiautomator-dump",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        pathErr,
		}
	}
	dumpArgs, dumpArgsErr := adb.UIAutomatorDumpFileArgv(devicePath)
	if dumpArgsErr != nil {
		return dumpPayload{}, latency, false, &adb.OperationError{
			Op:           "uiautomator-dump",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        dumpArgsErr,
		}
	}

	dumped, dumpErr := a.commands.RunAllowlisted(ctx, serial, dumpArgs)
	latency += dumped.Duration
	// Cleanup is unconditional from this point: the dump command may have
	// created the file even when it reported failure. Cancellation must not
	// skip it, so cleanup uses a context detached from the caller's.
	cleanupFailed := a.removeDeviceFile(ctx, serial, devicePath, &latency)
	if dumpErr != nil {
		return dumpPayload{}, latency, cleanupFailed, dumpErr
	}

	catArgs, catArgsErr := adb.CatArgv(devicePath)
	if catArgsErr != nil {
		return dumpPayload{}, latency, cleanupFailed, &adb.OperationError{
			Op:           "uiautomator-dump",
			Serial:       serial,
			FailureClass: domain.FailureInfrastructure,
			Cause:        catArgsErr,
		}
	}
	read, readErr := a.commands.RunAllowlisted(ctx, serial, catArgs)
	latency += read.Duration
	if readErr != nil {
		return dumpPayload{}, latency, cleanupFailed, readErr
	}
	hierarchy, ok := extractHierarchy(read.Stdout)
	if !ok {
		return dumpPayload{}, latency, cleanupFailed, &adb.OperationError{
			Op:           "uiautomator-dump",
			Serial:       serial,
			FailureClass: domain.FailureObservation,
			Detail:       adb.RedactOutput(read.Stdout),
			Cause:        ErrDumpEmpty,
		}
	}
	return dumpPayload{raw: hierarchy, truncated: read.StdoutTruncated}, latency, cleanupFailed, nil
}

// removeDeviceFile deletes the adapter-owned temporary file. It runs even when
// the caller's context is already done so no device-side residue is left.
func (a *Adapter) removeDeviceFile(ctx context.Context, serial, devicePath string, latency *time.Duration) bool {
	removeArgs, err := adb.RemoveArgv(devicePath)
	if err != nil {
		return true
	}
	cleanupCtx := context.WithoutCancel(ctx)
	removed, removeErr := a.commands.RunAllowlisted(cleanupCtx, serial, removeArgs)
	*latency += removed.Duration
	return removeErr != nil
}

func isFatal(err error) bool {
	switch adb.FailureClassOf(err) {
	case domain.FailureInfrastructure, domain.FailureTimeout, domain.FailureOperatorCancelled, domain.FailureDeviceOffline:
		return true
	default:
		return false
	}
}

func failureClassFor(err error) domain.FailureClass {
	if class := adb.FailureClassOf(err); class != "" {
		return class
	}
	return domain.FailureObservation
}

// extractHierarchy isolates the hierarchy document from mixed command output.
// uiautomator writes progress text such as "UI hierarchy dumped to: ..." on the
// same stream, and exec-out to /dev/tty appends a trailing status line.
func extractHierarchy(output []byte) ([]byte, bool) {
	start := bytes.Index(output, []byte("<hierarchy"))
	if start < 0 {
		return nil, false
	}
	if declaration := bytes.Index(output[:start], []byte("<?xml")); declaration >= 0 {
		start = declaration
	}
	end := bytes.LastIndex(output, []byte("</hierarchy>"))
	if end < 0 {
		// Keep the truncated remainder; the parser reports it as partial.
		return output[start:], true
	}
	return output[start : end+len("</hierarchy>")], true
}

type parseOutcome struct {
	nodes     []adapter.TargetNode
	maxDepth  int
	truncated bool
}

var boundsPattern = regexp.MustCompile(`^\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]$`)

// parse walks the hierarchy within the configured node and depth bounds. A
// node beyond a bound is dropped together with its subtree and the outcome is
// marked truncated; nothing is silently reshaped.
func (a *Adapter) parse(raw []byte) (parseOutcome, error) {
	outcome := parseOutcome{nodes: make([]adapter.TargetNode, 0, 64)}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = true

	depth := 0
	path := make([]string, 0, 32)

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return outcome, fmt.Errorf("%w: %v", ErrDumpMalformed, err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Local != "node" {
				continue
			}
			depth++
			if depth > a.maxDepth || len(outcome.nodes) >= a.maxNodes {
				outcome.truncated = true
				if skipErr := decoder.Skip(); skipErr != nil {
					return outcome, fmt.Errorf("%w: %v", ErrDumpMalformed, skipErr)
				}
				depth--
				continue
			}
			attributes := attributeMap(element)
			outcome.nodes = append(outcome.nodes, buildNode(attributes, path, depth))
			if depth > outcome.maxDepth {
				outcome.maxDepth = depth
			}
			path = append(path, pathSegment(attributes))
		case xml.EndElement:
			if element.Name.Local != "node" {
				continue
			}
			depth--
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		}
	}
	if depth != 0 {
		return outcome, fmt.Errorf("%w: %d unclosed nodes", ErrDumpMalformed, depth)
	}
	return outcome, nil
}

func attributeMap(element xml.StartElement) map[string]string {
	attributes := make(map[string]string, len(element.Attr))
	for _, attribute := range element.Attr {
		attributes[attribute.Name.Local] = attribute.Value
	}
	return attributes
}

func buildNode(attributes map[string]string, path []string, depth int) adapter.TargetNode {
	password := attributeBool(attributes, "password")
	className := attributes["class"]
	node := adapter.TargetNode{
		ResourceID:         attributes["resource-id"],
		AccessibilityLabel: sanitize(attributes["content-desc"], password),
		StableText:         sanitize(attributes["text"], password),
		ContextFingerprint: fingerprint(path, className, attributes["resource-id"], depth),
		ClassName:          className,
		Bounds:             parseBounds(attributes["bounds"]),
		Actionable: attributeBool(attributes, "clickable") ||
			attributeBool(attributes, "long-clickable") ||
			attributeBool(attributes, "scrollable") ||
			attributeBool(attributes, "checkable"),
		Enabled:  attributeBool(attributes, "enabled"),
		Editable: password || strings.HasSuffix(className, "EditText"),
	}
	return node
}

// sanitize removes credential material from observed text. A password field's
// contents are replaced outright rather than pattern-matched.
func sanitize(value string, password bool) string {
	if value == "" {
		return ""
	}
	if password {
		return redaction.Replacement
	}
	return redaction.RedactString(value)
}

func attributeBool(attributes map[string]string, key string) bool {
	value, ok := attributes[key]
	if !ok {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return parsed
}

func parseBounds(value string) [4]int {
	match := boundsPattern.FindStringSubmatch(value)
	if len(match) != 5 {
		return [4]int{}
	}
	var bounds [4]int
	for index := 0; index < 4; index++ {
		parsed, err := strconv.Atoi(match[index+1])
		if err != nil {
			return [4]int{}
		}
		bounds[index] = parsed
	}
	return bounds
}

// pathSegment identifies a node within its parent for fingerprinting. It never
// includes observed text, which may carry user content.
func pathSegment(attributes map[string]string) string {
	if resourceID := attributes["resource-id"]; resourceID != "" {
		return resourceID
	}
	return attributes["class"] + "[" + attributes["index"] + "]"
}

// fingerprint is a stable structural identity for a node. It excludes text and
// bounds so it survives content and layout changes.
func fingerprint(path []string, className, resourceID string, depth int) string {
	material := strings.Join(path, ">") + "|" + className + "|" + resourceID + "|" + strconv.Itoa(depth)
	return "sha256:" + hashHex([]byte(material))[:32]
}

func hashHex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
