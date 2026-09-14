// Package recordings owns the local interaction-recorder session boundary.
// It stores typed, sanitized evidence metadata; evidence bytes belong to the
// application-managed artifact store and are never held by this package.
package recordings

import (
	"fmt"
	"strings"
	"time"

	"drift.local/drift-next/internal/action"
	"drift.local/drift-next/internal/devices"
	"drift.local/drift-next/internal/domain"
	"drift.local/drift-next/internal/organizations"
	"drift.local/drift-next/internal/platform/redaction"
)

type RecordingSessionID string
type RecordingEventID string

// RecordingSession is kept as an alias for callers that use the persisted
// resource name. Session is the shorter domain name used internally.
type RecordingSession = Session

// RecordingEvent is the persisted logical BEFORE/ACTION/AFTER event.
type RecordingEvent = InteractionEvent

type SessionState string

const (
	SessionRequested SessionState = "requested"
	SessionRecording SessionState = "recording"
	SessionStopping  SessionState = "stopping"
	SessionCompleted SessionState = "completed"
	SessionDiscarded SessionState = "discarded"
	SessionFailed    SessionState = "failed"
)

type Source string

const (
	SourceFake    Source = "fake"
	SourceDevice  Source = "device"
	SourceMirror  Source = "mirror"
	SourceUnknown Source = "unknown"
)

type CleanupState string

const (
	CleanupNone    CleanupState = "none"
	CleanupPending CleanupState = "pending"
	CleanupDone    CleanupState = "done"
	CleanupFailed  CleanupState = "failed"
)

type ReviewState string

const (
	ReviewUnreviewed ReviewState = "unreviewed"
	ReviewApproved   ReviewState = "approved"
	ReviewRejected   ReviewState = "rejected"
)

type RedactionState string

const (
	RedactionNotRequired RedactionState = "not_required"
	RedactionRequired    RedactionState = "required"
	RedactionRedacted    RedactionState = "redacted"
	RedactionRejected    RedactionState = "rejected"
)

type CaptureStatus string

const (
	CaptureComplete CaptureStatus = "complete"
	CapturePartial  CaptureStatus = "partial"
	CaptureFailed   CaptureStatus = "failed"
)

type SanitizationState string

const (
	Sanitized             SanitizationState = "sanitized"
	SanitizationNotNeeded SanitizationState = "not_needed"
	Unsanitizable         SanitizationState = "unsanitizable"
)

type CapturePhase string

const (
	PhaseBefore CapturePhase = "before"
	PhaseAction CapturePhase = "action"
	PhaseAfter  CapturePhase = "after"
	PhaseReview CapturePhase = "review"
)

type EvidenceKind string

const (
	EvidenceRawScreenshot       EvidenceKind = "raw_screenshot"
	EvidenceAnnotatedScreenshot EvidenceKind = "annotated_screenshot"
	EvidenceUITree              EvidenceKind = "ui_tree"
	EvidenceOCR                 EvidenceKind = "ocr"
	EvidenceTrace               EvidenceKind = "trace"
	EvidenceLog                 EvidenceKind = "log"
)

type RawInputKind string

const (
	InputTap         RawInputKind = "tap"
	InputText        RawInputKind = "text_input"
	InputTextDelete  RawInputKind = "text_delete"
	InputClear       RawInputKind = "clear"
	InputSwipe       RawInputKind = "swipe"
	InputScroll      RawInputKind = "scroll"
	InputDrag        RawInputKind = "drag"
	InputBack        RawInputKind = "back"
	InputHome        RawInputKind = "home"
	InputEnter       RawInputKind = "enter"
	InputKey         RawInputKind = "key_event"
	InputUIChange    RawInputKind = "ui_change"
	InputStateChange RawInputKind = "state_change"
)

type Sensitivity string

const (
	SensitivityNone      Sensitivity = "none"
	SensitivitySensitive Sensitivity = "sensitive"
	SensitivityUncertain Sensitivity = "uncertain"
)

// Session is the durable lifecycle projection. DeletedAt is a tombstone: the
// session remains queryable for audit and skill provenance while its evidence
// cleanup is handled separately.
type Session struct {
	ID           RecordingSessionID
	Workspace    organizations.WorkspaceID
	AutomationID string
	DeviceID     devices.DeviceID
	Number       int64
	State        SessionState
	Source       Source
	StartedAt    *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	DeletedAt    *time.Time
	Cleanup      CleanupState
	Review       ReviewState
}

type EvidenceReference struct {
	Kind           EvidenceKind
	Phase          CapturePhase
	ArtifactID     string
	ContentHash    string
	MediaType      string
	SchemaVersion  int
	Authoritative  bool
	Omitted        bool
	OmissionReason string
}

type TargetMetadata struct {
	Semantic       action.SemanticTarget
	ClassName      string
	Bounds         [4]int
	Actionable     bool
	Enabled        bool
	Editable       bool
	Selected       bool
	Checked        bool
	Source         string
	Confidence     float64
	CandidateCount int
}

type Capture struct {
	ObservationID   string
	FreshnessToken  string
	CapturedAt      time.Time
	CoordinateSpace string
	PackageName     string
	ActivityName    string
	AppVersion      string
	DisplayWidth    int
	DisplayHeight   int
	Orientation     string
	ScreenshotHash  string
	UITreeHash      string
	Status          CaptureStatus
	Sanitization    SanitizationState
	Partial         bool
	ErrorClass      domain.FailureClass
	Target          *TargetMetadata
	Evidence        []EvidenceReference
}

type Gesture struct {
	Points     []action.Coordinate
	DurationMs int64
}

type LogicalAction struct {
	Kind                  action.Kind
	Target                TargetMetadata
	Coordinate            *action.Coordinate
	DisplayValue          string
	ValueLength           int
	Sensitivity           Sensitivity
	Gesture               *Gesture
	KeyCode               int
	IrreversibleConfirmed bool
	TimeoutMillis         int64
	Postcondition         string
}

type CaptureError struct {
	Phase   CapturePhase
	Class   domain.FailureClass
	Message string
	Omitted bool
}

type InteractionEvent struct {
	ID            RecordingEventID
	Workspace     organizations.WorkspaceID
	SessionID     RecordingSessionID
	Sequence      int
	CorrelationID string
	Action        LogicalAction
	Before        *Capture
	After         *Capture
	Evidence      []EvidenceReference
	CaptureErrors []CaptureError
	Redaction     RedactionState
	Sensitive     bool
	Review        ReviewState
	ReviewerID    string
	ReviewedAt    *time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
	CreatedAt     time.Time
}

type RawInput struct {
	DeviceID              devices.DeviceID
	Kind                  RawInputKind
	At                    time.Time
	EndAt                 time.Time
	Start                 action.Coordinate
	End                   *action.Coordinate
	Text                  string
	ValueLength           int
	Sensitivity           Sensitivity
	Target                TargetMetadata
	KeyCode               int
	IrreversibleConfirmed bool
}

func (s Source) Valid() bool {
	switch s {
	case SourceFake, SourceDevice, SourceMirror, SourceUnknown:
		return true
	default:
		return false
	}
}

func (s CleanupState) Valid() bool {
	return s == CleanupNone || s == CleanupPending || s == CleanupDone || s == CleanupFailed
}

func (s ReviewState) Valid() bool {
	return s == ReviewUnreviewed || s == ReviewApproved || s == ReviewRejected
}

func (s RedactionState) Valid() bool {
	return s == RedactionNotRequired || s == RedactionRequired || s == RedactionRedacted || s == RedactionRejected
}

func (s CaptureStatus) Valid() bool {
	return s == CaptureComplete || s == CapturePartial || s == CaptureFailed
}

func (s SanitizationState) Valid() bool {
	return s == Sanitized || s == SanitizationNotNeeded || s == Unsanitizable
}

func (p CapturePhase) Valid() bool {
	return p == PhaseBefore || p == PhaseAction || p == PhaseAfter || p == PhaseReview
}

func (k EvidenceKind) Valid() bool {
	switch k {
	case EvidenceRawScreenshot, EvidenceAnnotatedScreenshot, EvidenceUITree, EvidenceOCR, EvidenceTrace, EvidenceLog:
		return true
	default:
		return false
	}
}

func (s Sensitivity) Valid() bool {
	return s == SensitivityNone || s == SensitivitySensitive || s == SensitivityUncertain
}

func (k RawInputKind) Valid() bool {
	switch k {
	case InputTap, InputText, InputTextDelete, InputClear, InputSwipe, InputScroll, InputDrag, InputBack, InputHome, InputEnter, InputKey, InputUIChange, InputStateChange:
		return true
	default:
		return false
	}
}

func (s SessionState) Valid() bool {
	switch s {
	case SessionRequested, SessionRecording, SessionStopping, SessionCompleted, SessionDiscarded, SessionFailed:
		return true
	default:
		return false
	}
}

func CanTransitionSession(from, to SessionState) bool {
	switch from {
	case SessionRequested:
		return to == SessionRecording || to == SessionDiscarded || to == SessionFailed
	case SessionRecording:
		return to == SessionStopping || to == SessionDiscarded || to == SessionFailed
	case SessionStopping:
		return to == SessionCompleted || to == SessionDiscarded || to == SessionFailed
	case SessionCompleted, SessionDiscarded, SessionFailed:
		return false
	default:
		return false
	}
}

func TransitionSession(from, to SessionState) error {
	if !CanTransitionSession(from, to) {
		return domain.InvalidTransition("recording_session", string(from), string(to))
	}
	return nil
}

func (s Session) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" || strings.TrimSpace(string(s.Workspace)) == "" || s.Number < 0 || !s.State.Valid() || !s.Source.Valid() || !s.Cleanup.Valid() || !s.Review.Valid() || s.CreatedAt.IsZero() {
		return fmt.Errorf("recording session identity or state is invalid")
	}
	if len(s.AutomationID) > 256 || len(s.DeviceID) > 256 {
		return fmt.Errorf("recording session metadata is unbounded")
	}
	if s.State == SessionRequested && (s.StartedAt != nil || s.FinishedAt != nil) {
		return fmt.Errorf("requested sessions cannot have start or finish times")
	}
	if (s.State == SessionRecording || s.State == SessionStopping) && s.FinishedAt != nil {
		return fmt.Errorf("active sessions cannot have a finish time")
	}
	if s.State != SessionRequested && s.StartedAt == nil {
		return fmt.Errorf("non-requested sessions require a start time")
	}
	if s.State == SessionCompleted || s.State == SessionDiscarded || s.State == SessionFailed {
		if s.FinishedAt == nil {
			return fmt.Errorf("terminal sessions require a finish time")
		}
	}
	if s.StartedAt != nil && s.StartedAt.Before(s.CreatedAt) {
		return fmt.Errorf("session start precedes creation")
	}
	if s.FinishedAt != nil && s.StartedAt != nil && s.FinishedAt.Before(*s.StartedAt) {
		return fmt.Errorf("session finish precedes start")
	}
	if s.DeletedAt != nil && s.FinishedAt != nil && s.DeletedAt.Before(*s.FinishedAt) {
		return fmt.Errorf("session deletion precedes finish")
	}
	if s.DeletedAt != nil && s.State != SessionCompleted && s.State != SessionDiscarded {
		return fmt.Errorf("only completed or discarded sessions can be deleted")
	}
	return nil
}

func (e EvidenceReference) Validate() error {
	if !e.Kind.Valid() || !e.Phase.Valid() {
		return fmt.Errorf("evidence kind or phase is invalid")
	}
	if len(e.ArtifactID) > 256 || len(e.ContentHash) > 256 || len(e.MediaType) > 256 || len(e.OmissionReason) > 512 {
		return fmt.Errorf("evidence metadata is unbounded")
	}
	if e.Omitted {
		if strings.TrimSpace(e.OmissionReason) == "" || e.ArtifactID != "" || e.ContentHash != "" || e.Authoritative {
			return fmt.Errorf("omitted evidence must not carry bytes or authority")
		}
		return nil
	}
	if strings.TrimSpace(e.ArtifactID) == "" && strings.TrimSpace(e.ContentHash) == "" {
		return fmt.Errorf("evidence requires an artifact or content hash")
	}
	if strings.TrimSpace(e.MediaType) == "" || e.SchemaVersion <= 0 {
		return fmt.Errorf("evidence reference metadata is required")
	}
	combined := e.ArtifactID + e.ContentHash + e.MediaType + e.OmissionReason
	if redaction.RedactString(combined) != combined {
		return fmt.Errorf("evidence metadata is sensitive")
	}
	return nil
}

func (c Capture) Validate() error {
	if c.CapturedAt.IsZero() || len(c.ObservationID) > 256 || len(c.FreshnessToken) > 256 || len(c.CoordinateSpace) > 128 || len(c.PackageName) > 256 || len(c.ActivityName) > 256 || len(c.AppVersion) > 256 || len(c.Orientation) > 64 || len(c.ScreenshotHash) > 256 || len(c.UITreeHash) > 256 || c.DisplayWidth < 0 || c.DisplayHeight < 0 || !c.Status.Valid() || !c.Sanitization.Valid() {
		return fmt.Errorf("capture metadata is invalid or unbounded")
	}
	if c.Status == CaptureComplete && c.Partial {
		return fmt.Errorf("complete captures cannot be partial")
	}
	if c.Status != CaptureComplete && !c.Partial {
		return fmt.Errorf("partial or failed captures must be marked partial")
	}
	if c.Sanitization == Unsanitizable {
		if c.ScreenshotHash != "" || c.UITreeHash != "" || c.Target != nil {
			return fmt.Errorf("unsanitizable captures cannot retain evidence bytes or targets")
		}
		for _, evidence := range c.Evidence {
			if !evidence.Omitted {
				return fmt.Errorf("unsanitizable captures must omit evidence bytes")
			}
		}
	}
	if c.ErrorClass != "" && !c.ErrorClass.Valid() {
		return fmt.Errorf("capture error class is invalid")
	}
	for _, value := range []string{c.ObservationID, c.FreshnessToken, c.CoordinateSpace, c.PackageName, c.ActivityName, c.AppVersion, c.Orientation, c.ScreenshotHash, c.UITreeHash} {
		if redaction.RedactString(value) != value {
			return fmt.Errorf("capture metadata is sensitive")
		}
	}
	for _, evidence := range c.Evidence {
		if err := evidence.Validate(); err != nil {
			return err
		}
	}
	if c.Target != nil {
		if err := c.Target.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (t TargetMetadata) Validate() error {
	for _, value := range []string{t.Semantic.ResourceID, t.Semantic.AccessibilityLabel, t.Semantic.StableText, t.Semantic.ContextFingerprint, t.ClassName, t.Source} {
		if len(value) > 512 || redaction.RedactString(value) != value {
			return fmt.Errorf("target metadata is invalid or sensitive")
		}
	}
	if t.CandidateCount < 0 || t.CandidateCount > 1024 || t.Confidence < 0 || t.Confidence > 1 {
		return fmt.Errorf("target candidate metadata is invalid")
	}
	for _, bound := range t.Bounds {
		if bound < 0 || bound > 10000 {
			return fmt.Errorf("target bounds are invalid")
		}
	}
	return nil
}

func (g Gesture) Validate() error {
	if len(g.Points) < 2 || len(g.Points) > 128 || g.DurationMs < 0 || g.DurationMs > 5*60*1000 {
		return fmt.Errorf("gesture path is invalid")
	}
	space := g.Points[0].Space
	if strings.TrimSpace(space) == "" {
		return fmt.Errorf("gesture coordinate space is required")
	}
	for _, point := range g.Points {
		if point.Space != space || point.X < 0 || point.Y < 0 || point.X > 10000 || point.Y > 10000 {
			return fmt.Errorf("gesture coordinates are invalid")
		}
	}
	return nil
}

func (a LogicalAction) Validate() error {
	spec, ok := action.Lookup(a.Kind)
	if !ok {
		return fmt.Errorf("logical action is not allow-listed")
	}
	if err := a.Target.Validate(); err != nil {
		return err
	}
	if sensitivityTarget := sensitiveTarget(a.Target); sensitivityTarget && a.Sensitivity == SensitivityNone {
		return fmt.Errorf("sensitive targets require an explicit sensitivity state")
	}
	if !a.Sensitivity.Valid() || a.ValueLength < 0 || a.ValueLength > 1<<20 || len(a.DisplayValue) > 1024 || redaction.RedactString(a.DisplayValue) != a.DisplayValue {
		return fmt.Errorf("logical action value metadata is invalid or sensitive")
	}
	if a.Sensitivity != SensitivityNone && a.DisplayValue != redaction.Replacement {
		return fmt.Errorf("sensitive action values must use the redacted display marker")
	}
	if a.Gesture != nil {
		if err := a.Gesture.Validate(); err != nil {
			return err
		}
	}
	if a.Coordinate != nil && (strings.TrimSpace(a.Coordinate.Space) == "" || a.Coordinate.X < 0 || a.Coordinate.Y < 0 || a.Coordinate.X > 10000 || a.Coordinate.Y > 10000 || len(a.Coordinate.Space) > 128) {
		return fmt.Errorf("logical action coordinate is invalid")
	}
	if a.KeyCode < 0 || a.KeyCode > 10000 || a.TimeoutMillis < 0 || a.TimeoutMillis > 5*60*1000 || len(a.Postcondition) > 512 || redaction.RedactString(a.Postcondition) != a.Postcondition {
		return fmt.Errorf("logical action execution metadata is invalid")
	}
	if spec.Mutating && a.TimeoutMillis == 0 {
		return fmt.Errorf("mutating logical actions require a timeout")
	}
	if spec.Mutating && a.Postcondition == "" {
		return fmt.Errorf("mutating logical actions require a postcondition")
	}
	return nil
}

func (e CaptureError) Validate() error {
	if !e.Phase.Valid() || !e.Class.Valid() || len(e.Message) > 256 || strings.TrimSpace(e.Message) == "" || redaction.RedactString(e.Message) != e.Message {
		return fmt.Errorf("capture error is invalid or sensitive")
	}
	return nil
}

func (e InteractionEvent) Validate() error {
	if strings.TrimSpace(string(e.ID)) == "" || strings.TrimSpace(string(e.Workspace)) == "" || strings.TrimSpace(string(e.SessionID)) == "" || e.Sequence < 0 || strings.TrimSpace(e.CorrelationID) == "" || e.StartedAt.IsZero() || e.FinishedAt.IsZero() || e.CreatedAt.IsZero() || e.FinishedAt.Before(e.StartedAt) || !e.Redaction.Valid() || !e.Review.Valid() {
		return fmt.Errorf("recording event identity, time, or state is invalid")
	}
	if len(e.CorrelationID) > 256 || len(e.ReviewerID) > 256 {
		return fmt.Errorf("recording event metadata is unbounded")
	}
	if err := e.Action.Validate(); err != nil {
		return err
	}
	if e.Before != nil {
		if err := e.Before.Validate(); err != nil {
			return err
		}
	}
	if e.After != nil {
		if err := e.After.Validate(); err != nil {
			return err
		}
	}
	for _, evidence := range e.Evidence {
		if err := evidence.Validate(); err != nil {
			return err
		}
	}
	for _, captureError := range e.CaptureErrors {
		if err := captureError.Validate(); err != nil {
			return err
		}
	}
	if e.Sensitive || e.Action.Sensitivity != SensitivityNone {
		if e.Redaction != RedactionRedacted && e.Redaction != RedactionRejected {
			return fmt.Errorf("sensitive recording events require redaction")
		}
		if e.Action.DisplayValue != "" && e.Action.DisplayValue != redaction.Replacement {
			return fmt.Errorf("sensitive recording event contains plaintext")
		}
	} else if e.Redaction == RedactionRequired {
		return fmt.Errorf("non-sensitive event cannot require redaction")
	}
	if e.Review == ReviewApproved && e.ReviewerID == "" {
		return fmt.Errorf("approved events require a reviewer")
	}
	if e.ReviewedAt != nil && e.ReviewedAt.Before(e.CreatedAt) {
		return fmt.Errorf("review precedes event creation")
	}
	return nil
}

func (r RawInput) Validate() error {
	if strings.TrimSpace(string(r.DeviceID)) == "" || !r.Kind.Valid() || r.At.IsZero() || !r.Sensitivity.Valid() || len(r.Text) > 1<<20 || r.ValueLength < 0 || r.ValueLength > 1<<20 || r.KeyCode < 0 || r.KeyCode > 10000 {
		return fmt.Errorf("raw input is invalid or unbounded")
	}
	if !r.EndAt.IsZero() && r.EndAt.Before(r.At) {
		return fmt.Errorf("raw input end precedes start")
	}
	if r.Start.Space != "" && (r.Start.X < 0 || r.Start.Y < 0 || r.Start.X > 10000 || r.Start.Y > 10000) {
		return fmt.Errorf("raw input start coordinate is invalid")
	}
	if r.End != nil && (r.End.Space != r.Start.Space || r.End.X < 0 || r.End.Y < 0 || r.End.X > 10000 || r.End.Y > 10000) {
		return fmt.Errorf("raw input end coordinate is invalid")
	}
	if err := r.Target.Validate(); err != nil {
		return err
	}
	return nil
}

func CloneCapture(input *Capture) *Capture {
	if input == nil {
		return nil
	}
	result := *input
	result.Evidence = append([]EvidenceReference(nil), input.Evidence...)
	if input.Target != nil {
		target := *input.Target
		result.Target = &target
	}
	return &result
}

func CloneAction(input LogicalAction) LogicalAction {
	result := input
	if input.Gesture != nil {
		gesture := *input.Gesture
		gesture.Points = append([]action.Coordinate(nil), input.Gesture.Points...)
		result.Gesture = &gesture
	}
	if input.Coordinate != nil {
		coordinate := *input.Coordinate
		result.Coordinate = &coordinate
	}
	return result
}

func CloneEvent(input InteractionEvent) InteractionEvent {
	result := input
	result.Before = CloneCapture(input.Before)
	result.After = CloneCapture(input.After)
	result.Evidence = append([]EvidenceReference(nil), input.Evidence...)
	result.CaptureErrors = append([]CaptureError(nil), input.CaptureErrors...)
	result.Action = CloneAction(input.Action)
	return result
}
