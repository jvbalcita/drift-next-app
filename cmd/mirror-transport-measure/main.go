// Command mirror-transport-measure compares the two live-mirror transports this
// plane can carry - a negotiated WebRTC peer connection and the per-device
// container stream fetched from MirrorStreamPath - against a real device, and
// writes one JSON line per measurement.
//
// It exists because the choice of default transport is a decision that has to be
// made from a number taken on this fleet rather than from the shape of the
// device's address (ARC-231). What it measures, per run:
//
//   - open_ms: the plane's own StartMirrorStream call, which is where the device
//     capture is started or joined.
//   - handshake_ms: from the stream being open to the first picture arriving at
//     THIS client - the term the transport itself is responsible for.
//   - ttff_ms: from the request to the first picture, which is what an operator
//     waits for. It carries the capture start, which is common to both
//     transports, so it is reported beside handshake_ms rather than instead of it.
//   - keyframe_ms: to the first IDR, when the first picture was not one.
//   - steady state: pictures/fragments per second, the inter-arrival gap
//     distribution, the number of stalls longer than a second, and the stream's
//     own reported state at the end.
//
// It is a measurement tool, not a product surface: it speaks the plane's own
// guarded lab surface with the lab token, opens one viewer at a time, and stops
// each stream it opened before opening the next.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	connectrpc "connectrpc.com/connect"
	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/gen/go/drift/v1/driftv1connect"
	"drift.local/drift-next/internal/service"
	"github.com/pion/webrtc/v4"
)

// labTokenHeader is the plane's own guarded-surface header. It is read from the
// service package rather than spelled again, so a renamed header is a build
// failure here instead of a measurement that silently 401s.
const labTokenHeader = service.LabTokenHeader

type sample struct {
	Transport    string  `json:"transport"`
	Run          int     `json:"run"`
	DeviceID     string  `json:"device_id"`
	Serial       string  `json:"serial"`
	OpenMs       float64 `json:"open_ms"`
	HandshakeMs  float64 `json:"handshake_ms"`
	TTFFMs       float64 `json:"ttff_ms"`
	KeyframeMs   float64 `json:"keyframe_ms"`
	WindowS      float64 `json:"window_s"`
	Pictures     int     `json:"pictures"`
	PicturesPerS float64 `json:"pictures_per_s"`
	GapP50Ms     float64 `json:"gap_p50_ms"`
	GapP95Ms     float64 `json:"gap_p95_ms"`
	GapP99Ms     float64 `json:"gap_p99_ms"`
	GapMaxMs     float64 `json:"gap_max_ms"`
	StallsOver1s int     `json:"stalls_over_1s"`
	State        string  `json:"state"`
	Failure      string  `json:"failure"`
	Error        string  `json:"error,omitempty"`
}

type runner struct {
	base      string
	token     string
	workspace string
	http      *http.Client
	discovery driftv1connect.DiscoveryServiceClient
	devices   driftv1connect.DeviceServiceClient
	mirror    driftv1connect.DeviceMirrorServiceClient
}

func newRunner(base, token, workspace string) *runner {
	httpClient := &http.Client{}
	interceptor := connectrpc.WithInterceptors(connectrpc.UnaryInterceptorFunc(
		func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
			return func(ctx context.Context, request connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
				request.Header().Set(labTokenHeader, token)
				return next(ctx, request)
			}
		}))
	return &runner{
		base:      strings.TrimRight(base, "/"),
		token:     token,
		workspace: workspace,
		http:      httpClient,
		discovery: driftv1connect.NewDiscoveryServiceClient(httpClient, base, interceptor),
		devices:   driftv1connect.NewDeviceServiceClient(httpClient, base, interceptor),
		mirror:    driftv1connect.NewDeviceMirrorServiceClient(httpClient, base, interceptor),
	}
}

func (r *runner) requestContext(key string) *driftv1.RequestContext {
	return &driftv1.RequestContext{RequestId: key, IdempotencyKey: key, ActorId: "mirror-transport-measure"}
}

func (r *runner) workspaceRef() *driftv1.WorkspaceRef {
	return &driftv1.WorkspaceRef{WorkspaceId: r.workspace}
}

// observe runs one bounded scan of the operator-entered range so the registry
// holds the devices this measurement mirrors. A scan is an observation: it upserts
// each observed device and returns it with its identity.
func (r *runner) observe(ctx context.Context, addressPolicy string, port uint32) ([]*driftv1.ObservedDevice, error) {
	response, err := r.discovery.StartRangeScan(ctx, connectrpc.NewRequest(&driftv1.StartRangeScanRequest{
		Context:       r.requestContext("measure-scan-" + time.Now().UTC().Format("150405.000")),
		Workspace:     r.workspaceRef(),
		AddressPolicy: addressPolicy,
		Port:          port,
	}))
	if err != nil {
		return nil, fmt.Errorf("start range scan: %w", err)
	}
	return response.Msg.GetDevices(), nil
}

// start opens one device's stream over the requested transport.
func (r *runner) start(ctx context.Context, deviceID string, transport driftv1.MirrorTransport) (*driftv1.MirrorStream, error) {
	response, err := r.mirror.StartMirrorStream(ctx, connectrpc.NewRequest(&driftv1.StartMirrorStreamRequest{
		Context:   r.requestContext("measure-start-" + time.Now().UTC().Format("150405.000000")),
		Workspace: r.workspaceRef(),
		DeviceId:  deviceID,
		Transport: transport,
		Purpose:   driftv1.MirrorViewerPurpose_MIRROR_VIEWER_PURPOSE_OPERATOR,
	}))
	if err != nil {
		return nil, err
	}
	return response.Msg.GetStream(), nil
}

func (r *runner) stop(ctx context.Context, streamID string) {
	_, _ = r.mirror.StopMirrorStream(ctx, connectrpc.NewRequest(&driftv1.StopMirrorStreamRequest{
		Context:  r.requestContext("measure-stop-" + time.Now().UTC().Format("150405.000000")),
		StreamId: streamID,
	}))
}

func (r *runner) state(ctx context.Context, streamID string) *driftv1.MirrorStream {
	response, err := r.mirror.GetMirrorStream(ctx, connectrpc.NewRequest(&driftv1.GetMirrorStreamRequest{StreamId: streamID}))
	if err != nil {
		return nil
	}
	return response.Msg.GetStream()
}

// startMotion drives one device's own screen for as long as a measurement runs,
// and returns the function that stops it.
//
// It exists because this fleet's encoder is change-driven: a still screen produces
// no pictures at all, so a steady-state window taken without motion would report
// zero frames per second on both transports and measure nothing. What it does to
// the device is a bounded swipe inside the render space the console uses - the
// `wm size` override - and nothing else; it is a measurement input, not a device
// action, and it goes through the same allow-listed adb the adapter uses.
func startMotion(ctx context.Context, adbPath, serial string) func() {
	motionCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	go func() {
		// The loop runs ON the device, not on this host: one long-lived adb
		// process drives the screen, so the host pays no adb round trip per
		// picture and the measurement is not charged for the driver's own cost.
		script := "while true; do input swipe 540 1700 540 1100 40; input swipe 540 1100 540 1700 40; done"
		command := exec.CommandContext(motionCtx, adbPath, "-s", serial, "shell", script)
		_ = command.Run()
	}()
	return func() { once.Do(cancel) }
}

func main() {
	address := flag.String("addr", "http://127.0.0.1:8099", "control-plane base URL")
	token := flag.String("token", "", "lab token")
	workspace := flag.String("workspace", "workspace-lab-local", "workspace id")
	addressPolicy := flag.String("range", "192.168.1.100-192.168.1.140", "inclusive IPv4 range to observe")
	port := flag.Uint("port", 5555, "transport port the range is observed on")
	wantedSerial := flag.String("serial", "", "serial to mirror; empty picks the first observed device")
	runs := flag.Int("runs", 3, "time-to-first-picture runs per transport")
	window := flag.Float64("window", 60, "steady-state window in seconds per run")
	settle := flag.Float64("settle", 4, "seconds to wait between runs, so a previous capture has stopped")
	// Motion: this fleet's screen encoder emits nothing while the screen is
	// static, so a steady-state measurement taken on an idle device measures the
	// encoder's idle behaviour rather than the transport. The driver swipes the
	// device's own screen for the length of each measurement and stops with it.
	motion := flag.Bool("motion", true, "drive the device's screen during each measurement, so the encoder has frames to carry")
	adbPath := flag.String("adb", "adb", "adb executable used for the motion driver")
	only := flag.String("only", "", "comma-separated transports to measure (webrtc,tcp); empty measures both in that order")
	out := flag.String("out", "", "JSONL output path; empty writes to stdout")
	flag.Parse()

	if strings.TrimSpace(*token) == "" {
		fmt.Fprintln(os.Stderr, "a lab token is required: the plane's surfaces are token-guarded")
		os.Exit(2)
	}

	ctx := context.Background()
	r := newRunner(*address, *token, *workspace)

	observed, err := r.observe(ctx, *addressPolicy, uint32(*port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "observation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "observed %d device(s)\n", len(observed))

	var target *driftv1.ObservedDevice
	for _, device := range observed {
		if *wantedSerial == "" || device.GetSerial() == *wantedSerial {
			target = device
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "no observed device matched serial %q\n", *wantedSerial)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "mirroring %s (%s) over %s:%d\n", target.GetDeviceId(), target.GetSerial(), target.GetHost(), target.GetPort())

	var writer io.Writer = os.Stdout
	if *out != "" {
		file, createErr := os.Create(*out)
		if createErr != nil {
			fmt.Fprintf(os.Stderr, "cannot write %s: %v\n", *out, createErr)
			os.Exit(1)
		}
		defer func() { _ = file.Close() }()
		writer = file
	}
	encoder := json.NewEncoder(writer)

	transports := []struct {
		name string
		kind driftv1.MirrorTransport
	}{
		{"webrtc", driftv1.MirrorTransport_MIRROR_TRANSPORT_WEBRTC},
		{"tcp", driftv1.MirrorTransport_MIRROR_TRANSPORT_TCP},
	}
	if *only != "" {
		selected := make([]struct {
			name string
			kind driftv1.MirrorTransport
		}, 0, 2)
		for _, name := range strings.Split(*only, ",") {
			for _, transport := range transports {
				if strings.TrimSpace(name) == transport.name {
					selected = append(selected, transport)
				}
			}
		}
		if len(selected) == 0 {
			fmt.Fprintf(os.Stderr, "-only %q named no known transport\n", *only)
			os.Exit(2)
		}
		transports = selected
	}

	for _, transport := range transports {
		for run := 1; run <= *runs; run++ {
			stopMotion := func() {}
			if *motion {
				stopMotion = startMotion(ctx, *adbPath, target.GetSerial())
			}
			measurement := r.measureOne(ctx, target, transport.name, transport.kind, run, *window)
			stopMotion()
			if err := encoder.Encode(measurement); err != nil {
				fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			}
			_ = writer.(interface{ Sync() error })
			fmt.Fprintf(os.Stderr, "%s run %d: ttff=%.0fms handshake=%.0fms pictures=%d failure=%q err=%v\n",
				measurement.Transport, measurement.Run, measurement.TTFFMs, measurement.HandshakeMs,
				measurement.Pictures, measurement.Failure, measurement.Error)
			time.Sleep(time.Duration(*settle * float64(time.Second)))
		}
	}
}

// measureOne opens one stream, waits for its first picture, then watches it for
// the steady-state window and reports what it carried.
func (r *runner) measureOne(ctx context.Context, device *driftv1.ObservedDevice, name string, kind driftv1.MirrorTransport, run int, window float64) sample {
	result := sample{
		Transport: name,
		Run:       run,
		DeviceID:  device.GetDeviceId(),
		Serial:    device.GetSerial(),
		WindowS:   window,
	}
	requestedAt := time.Now()
	stream, err := r.start(ctx, device.GetDeviceId(), kind)
	if err != nil {
		result.Error = "start: " + err.Error()
		return result
	}
	openedAt := time.Now()
	result.OpenMs = float64(openedAt.Sub(requestedAt).Microseconds()) / 1000
	streamID := stream.GetStreamId()
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r.stop(stopCtx, streamID)
	}()

	watchCtx, cancel := context.WithTimeout(ctx, time.Duration((window+30)*float64(time.Second)))
	defer cancel()

	var arrivals []time.Time
	var firstPicture, firstKeyframe time.Time
	switch name {
	case "webrtc":
		arrivals, firstPicture, firstKeyframe, err = watchWebRTC(watchCtx, r, streamID, stream.GetStreamUrl(), window)
	case "tcp":
		arrivals, firstPicture, firstKeyframe, err = watchTCP(watchCtx, r, stream.GetStreamUrl(), window)
	}
	if err != nil {
		result.Error = err.Error()
	}
	if !firstPicture.IsZero() {
		result.HandshakeMs = float64(firstPicture.Sub(openedAt).Microseconds()) / 1000
		result.TTFFMs = float64(firstPicture.Sub(requestedAt).Microseconds()) / 1000
	}
	if !firstKeyframe.IsZero() {
		result.KeyframeMs = float64(firstKeyframe.Sub(openedAt).Microseconds()) / 1000
	}
	result.Pictures = len(arrivals)
	result.PicturesPerS = float64(len(arrivals)) / window
	fillGaps(&result, arrivals)

	if state := r.state(context.Background(), streamID); state != nil {
		result.State = state.GetState().String()
		result.Failure = state.GetFailure()
	}
	return result
}

// fillGaps reports the inter-arrival distribution and the stalls in it. A gap is
// how long the client waited between two pictures, which is what a viewer sees as
// a stutter; it is not glass-to-glass latency, and it is not reported as one.
func fillGaps(result *sample, arrivals []time.Time) {
	if len(arrivals) < 2 {
		return
	}
	gaps := make([]float64, 0, len(arrivals)-1)
	for index := 1; index < len(arrivals); index++ {
		ms := float64(arrivals[index].Sub(arrivals[index-1]).Microseconds()) / 1000
		gaps = append(gaps, ms)
		if ms > 1000 {
			result.StallsOver1s++
		}
	}
	sort.Float64s(gaps)
	result.GapP50Ms = percentile(gaps, 0.50)
	result.GapP95Ms = percentile(gaps, 0.95)
	result.GapP99Ms = percentile(gaps, 0.99)
	result.GapMaxMs = gaps[len(gaps)-1]
}

func percentile(sorted []float64, fraction float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(fraction * float64(len(sorted)-1))
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

// watchWebRTC is the browser's half of the WebRTC path: it creates the offer,
// has the plane answer it, and reads pictures off the track. The first picture is
// the first RTP packet that carries a decodable H.264 access unit, and the first
// keyframe is the first IDR, which is what a viewer needs before anything after
// it decodes at all.
func watchWebRTC(ctx context.Context, r *runner, streamID, streamURL string, window float64) ([]time.Time, time.Time, time.Time, error) {
	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterDefaultCodecs(); err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("register codecs: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(engine))
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("new peer connection: %w", err)
	}
	defer func() { _ = peer.Close() }()
	if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("add transceiver: %w", err)
	}

	pictures := make(chan time.Time, 4096)
	keyframes := make(chan time.Time, 16)
	peer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			packet, _, readErr := track.ReadRTP()
			if readErr != nil {
				return
			}
			if len(packet.Payload) == 0 {
				continue
			}
			// One picture is one access unit, and the marker bit is what says the
			// unit ended: an H.264 access unit this size is fragmented across
			// several RTP packets, so counting packets would count one picture ten
			// times and report a frame rate this fleet does not have.
			nalType := packet.Payload[0] & 0x1f
			fragmentStart := true
			if nalType == 28 && len(packet.Payload) > 1 {
				nalType = packet.Payload[1] & 0x1f
				fragmentStart = packet.Payload[1]&0x80 != 0
			}
			if fragmentStart && nalType == 5 {
				select {
				case keyframes <- time.Now():
				default:
				}
			}
			if packet.Marker {
				select {
				case pictures <- time.Now():
				default:
				}
			}
		}
	})

	offer, err := peer.CreateOffer(nil)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("create offer: %w", err)
	}
	if err := peer.SetLocalDescription(offer); err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("set local description: %w", err)
	}
	<-webrtc.GatheringCompletePromise(peer)
	local := peer.LocalDescription()
	if local == nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("no local description after gathering")
	}

	answer, err := r.mirror.NegotiateMirrorStream(ctx, connectrpc.NewRequest(&driftv1.NegotiateMirrorStreamRequest{
		Context:  r.requestContext("measure-negotiate-" + time.Now().UTC().Format("150405.000000")),
		StreamId: streamID,
		OfferSdp: local.SDP,
	}))
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("negotiate: %w", err)
	}
	if err := peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.Msg.GetAnswerSdp()}); err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("set remote description: %w", err)
	}

	deadline := time.Now().Add(time.Duration(window * float64(time.Second)))
	var arrivals []time.Time
	var firstPicture, firstKeyframe time.Time
	for time.Now().Before(deadline) {
		select {
		case at := <-keyframes:
			if firstKeyframe.IsZero() {
				firstKeyframe = at
			}
		case at := <-pictures:
			if firstPicture.IsZero() {
				firstPicture = at
			}
			arrivals = append(arrivals, at)
		case <-ctx.Done():
			return arrivals, firstPicture, firstKeyframe, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return arrivals, firstPicture, firstKeyframe, nil
}

// watchTCP is the browser's half of the TCP path: it fetches the per-device
// stream endpoint and reads the fragmented MP4 the plane serves from it. The
// first picture is the moment the first media fragment has been read whole -
// the initialisation segment alone is not a picture, and a fragment read halfway
// is not one either.
func watchTCP(ctx context.Context, r *runner, streamURL string, window float64) ([]time.Time, time.Time, time.Time, error) {
	if strings.TrimSpace(streamURL) == "" {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("the plane served no stream url for the tcp transport")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+streamURL, nil)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("build stream request: %w", err)
	}
	request.Header.Set(labTokenHeader, r.token)
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("fetch stream: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return nil, time.Time{}, time.Time{}, fmt.Errorf("stream endpoint answered %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	reader := bufio.NewReaderSize(response.Body, 1<<20)

	// The container is read on its own goroutine so the window's clock belongs to
	// the caller: a device whose screen is static legitimately produces no fragment
	// for a while (this fleet's encoder stops when nothing changes), and a reader
	// that blocked on the socket would report the window as a failure instead of as
	// the gap it is.
	type boxRead struct {
		kind string
		at   time.Time
		err  error
	}
	boxes := make(chan boxRead, 64)
	readCtx, stopReading := context.WithCancel(ctx)
	defer stopReading()
	go func() {
		defer close(boxes)
		for {
			if readCtx.Err() != nil {
				return
			}
			boxType, err := readBox(reader)
			if err != nil {
				select {
				case boxes <- boxRead{err: err}:
				case <-readCtx.Done():
				}
				return
			}
			select {
			case boxes <- boxRead{kind: boxType, at: time.Now()}:
			case <-readCtx.Done():
				return
			}
		}
	}()

	deadline := time.Now().Add(time.Duration(window * float64(time.Second)))
	var arrivals []time.Time
	var firstPicture, firstKeyframe time.Time
	sawMoof := false
	for time.Now().Before(deadline) {
		select {
		case box, open := <-boxes:
			if !open {
				if firstPicture.IsZero() {
					return arrivals, firstPicture, firstKeyframe, fmt.Errorf("the stream endpoint ended before it carried a picture")
				}
				return arrivals, firstPicture, firstKeyframe, nil
			}
			if box.err != nil {
				if firstPicture.IsZero() {
					return arrivals, firstPicture, firstKeyframe, fmt.Errorf("read box: %w", box.err)
				}
				return arrivals, firstPicture, firstKeyframe, nil
			}
			switch box.kind {
			case "ftyp", "moov", "styp", "sidx":
				// The initialisation segment is not a picture: it carries the
				// parameters a decoder needs and no frame at all.
				if os.Getenv("DRIFT_MTM_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "  [tcp] box %s at %s\n", box.kind, box.at.Format("15:04:05.000"))
				}
			case "moof":
				sawMoof = true
			case "mdat":
				if !sawMoof {
					continue
				}
				sawMoof = false
				if firstPicture.IsZero() {
					firstPicture = box.at
					firstKeyframe = box.at
				}
				arrivals = append(arrivals, box.at)
				if os.Getenv("DRIFT_MTM_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "  [tcp] picture #%d at %s\n", len(arrivals), box.at.Format("15:04:05.000"))
				}
			}
		case <-ctx.Done():
			return arrivals, firstPicture, firstKeyframe, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return arrivals, firstPicture, firstKeyframe, nil
}

// readBox consumes one ISO-BMFF box and returns its type. A box with a 64-bit
// size is read as the large-size form rather than being mis-sized as zero.
func readBox(reader *bufio.Reader) (string, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", err
	}
	size := uint64(header[0])<<24 | uint64(header[1])<<16 | uint64(header[2])<<8 | uint64(header[3])
	boxType := string(header[4:8])
	body := int64(size) - 8
	if size == 1 {
		large := make([]byte, 8)
		if _, err := io.ReadFull(reader, large); err != nil {
			return "", err
		}
		size = 0
		for _, b := range large {
			size = size<<8 | uint64(b)
		}
		body = int64(size) - 16
	}
	if size == 0 {
		// A zero size means "to the end of the file", which a live stream never
		// ends: it is not a box this reader can bound.
		return "", fmt.Errorf("box %q declares no length", boxType)
	}
	if body < 0 {
		return "", fmt.Errorf("box %q declares an impossible length", boxType)
	}
	if body > 0 {
		if _, err := io.CopyN(io.Discard, reader, body); err != nil {
			return "", err
		}
	}
	return boxType, nil
}
