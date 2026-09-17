// Browser half of the ARC-144 Part B rig: the console-side observer.
//
// It answers one question per presented frame: which device-clock value is in
// this frame's pixels, and when did the browser expect to display it? The
// device page paints the millisecond wall clock as a 30x2 cell strip; this page
// reads that strip back out of the decoded video and records the pair. Latency
// is then (paint time) - (clock in that frame's pixels) - (measured device/host
// skew), which is glass-to-glass as far as the device's own pixels define it.
//
// The instrument must stay cheap: Part A showed a 1:1 readback stalls the
// pipeline and reports its own cost. Here the strip is drawn into a 30x2 canvas
// with smoothing OFF, so each output pixel is one cell-centre source pixel, and
// only 240 bytes are read per frame. The readback cost is measured and reported
// with the results.
'use strict';

const STRIP = { cellsX: 30, cellsY: 2, sync: [1, 0, 1, 0, 1, 0, 1, 0], msBits: 48, parityPos: 59 };
const BITS = STRIP.cellsX * STRIP.cellsY;

const logEl = document.getElementById('log');
function log(msg) {
  const line = `${((performance.timeOrigin + performance.now()) / 1000).toFixed(3)} ${msg}`;
  logEl.textContent += line + '\n';
  console.log('[live] ' + msg);
}
const abs = (t) => performance.timeOrigin + t;

// decodeStrip reads one cell strip out of the video element at the geometry the
// host located, and returns the decoded value or a reason it was refused.
//
// A refused read is reported as no sample rather than a guess: the strip carries
// its own parity, so a single bad cell cannot become a plausible timestamp.
function decodeStrip(video, ctx, geom) {
  ctx.imageSmoothingEnabled = false;
  ctx.drawImage(
    video,
    geom.x, geom.y, STRIP.cellsX * geom.cell, STRIP.cellsY * geom.cell,
    0, 0, STRIP.cellsX, STRIP.cellsY,
  );
  const d = ctx.getImageData(0, 0, STRIP.cellsX, STRIP.cellsY).data;
  const bits = new Array(BITS);
  for (let i = 0; i < BITS; i++) bits[i] = d[i * 4] >= 128 ? 1 : 0;
  for (let i = 0; i < STRIP.sync.length; i++) {
    if (bits[i] !== STRIP.sync[i]) return { err: 'sync' };
  }
  let parity = 0;
  for (let i = 0; i < STRIP.parityPos; i++) parity ^= bits[i];
  if (parity !== bits[STRIP.parityPos]) return { err: 'parity' };
  let lo = 0, hi = 0;
  for (let i = 0; i < 32; i++) lo += bits[8 + i] * Math.pow(2, i);
  for (let i = 0; i < 16; i++) hi += bits[40 + i] * Math.pow(2, i);
  return {
    ms: hi * 4294967296 + lo,
    reacting: bits[56] === 1,
    tapCount: bits[57] + bits[58] * 2,
  };
}

async function iceComplete(pc) {
  if (pc.iceGatheringState === 'complete') return;
  await new Promise((resolve) => {
    const check = () => {
      if (pc.iceGatheringState === 'complete') {
        pc.removeEventListener('icegatheringstatechange', check);
        resolve();
      }
    };
    pc.addEventListener('icegatheringstatechange', check);
    setTimeout(resolve, 5000);
  });
}

function rtcpSnapshot(report) {
  const out = {};
  report.forEach((s) => {
    if (s.type === 'inbound-rtp' && (s.kind === 'video' || s.mediaType === 'video')) {
      out.inbound = {
        codec: s.codecId, framesReceived: s.framesReceived, framesDecoded: s.framesDecoded,
        framesDropped: s.framesDropped, keyFramesDecoded: s.keyFramesDecoded,
        freezeCount: s.freezeCount, totalFreezesDuration: s.totalFreezesDuration,
        packetsReceived: s.packetsReceived, packetsLost: s.packetsLost,
        bytesReceived: s.bytesReceived, jitter: s.jitter,
        jitterBufferDelay: s.jitterBufferDelay, jitterBufferEmittedCount: s.jitterBufferEmittedCount,
        totalDecodeTime: s.totalDecodeTime, totalProcessingDelay: s.totalProcessingDelay,
        totalAssemblyTime: s.totalAssemblyTime,
        estimatedPlayoutTimestamp: s.estimatedPlayoutTimestamp,
        lastPacketReceivedTimestamp: s.lastPacketReceivedTimestamp,
        timestamp: s.timestamp,
      };
    }
    if (s.type === 'codec' && s.mimeType) {
      out.codec = { mimeType: s.mimeType, clockRate: s.clockRate, payloadType: s.payloadType };
    }
    if (s.type === 'transport') {
      out.transport = { bytesReceived: s.bytesReceived, dtlsState: s.dtlsState };
    }
  });
  return out;
}

// waitForStrip polls until the host has found the clock strip in a screenshot.
//
// The probe cannot start on an empty geometry: the calibration and every sample
// are read at that origin, and a run started before it exists would report a
// decode rate of zero while looking like it ran.
async function waitForStrip(timeoutMs) {
  const deadline = performance.now() + timeoutMs;
  let cfg = null;
  for (;;) {
    cfg = await fetch('/config', { cache: 'no-store' }).then((r) => r.json());
    if (cfg.strip && cfg.strip.found && cfg.strip.cell > 0) return cfg;
    if (performance.now() > deadline) {
      cfg.stripError = 'host never located the strip';
      return cfg;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
}

async function main() {
  const cfg = await waitForStrip(90000);
  log(`config: ${JSON.stringify(cfg)}`);

  const video = document.getElementById('video');
  const canvas = document.createElement('canvas');
  canvas.width = STRIP.cellsX;
  canvas.height = STRIP.cellsY;
  const ctx = canvas.getContext('2d', { willReadFrequently: true, alpha: false });

  const out = {
    transport: 'rtp', cfg, samples: [], stats: [], errors: [],
    offsets: [], geometry: null, userAgent: navigator.userAgent,
  };

  const pc = new RTCPeerConnection({
    iceServers: [],
    // Keep the receive buffer as small as the browser will allow: the number
    // under measurement is a "feels physically present" latency, so the honest
    // configuration is the low-latency one, not a buffered one.
    // (Chrome exposes no playout-delay knob to the page; it is recorded here so
    // the configuration is stated rather than implied.)
  });
  out.pcOptions = { iceServers: [], playoutDelayHint: 'not settable from a page in this Chrome' };
  const track = new Promise((resolve) => {
    pc.ontrack = (ev) => {
      out.ontrackFired = true;
      out.ontrackStreams = ev.streams ? ev.streams.length : 0;
      log(`ontrack fired: streams=${out.ontrackStreams} track=${ev.track ? ev.track.kind : 'none'}`);
      video.srcObject = ev.streams && ev.streams.length ? ev.streams[0] : new MediaStream([ev.track]);
      video.play().then(() => log('video.play() started'), (e) => log('play() rejected: ' + e));
      resolve(video.srcObject);
    };
  });
  pc.addTransceiver('video', { direction: 'recvonly' });
  const connected = new Promise((resolve) => {
    pc.onconnectionstatechange = () => {
      out.connectionState = pc.connectionState;
      log('connection state ' + pc.connectionState);
      if (pc.connectionState === 'connected') resolve();
    };
  });

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  await iceComplete(pc);
  const answer = await fetch('/offer', {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ sdp: pc.localDescription.sdp }),
  }).then((r) => r.json());
  out.answerSDP = answer.sdp;
  await pc.setRemoteDescription({ type: 'answer', sdp: answer.sdp });
  await Promise.race([connected, new Promise((r) => setTimeout(r, 10000))]);
  await Promise.race([track, new Promise((r) => setTimeout(r, 5000))]);
  out.ready = true;
  out.codecParams = pc.getReceivers()[0] ? pc.getReceivers()[0].getParameters() : null;

  // Attaching the receiver's track by hand is not redundant caution: on this
  // Chrome, `ontrack` never fired for this page, and a probe that waits for an
  // event that does not come reports an empty run as if the stream were empty.
  // The receiver's track is the same object ontrack would have handed over.
  await new Promise((r) => setTimeout(r, 3000));
  const receiver = pc.getReceivers()[0];
  out.transceiver = pc.getTransceivers()[0] ? {
    mid: pc.getTransceivers()[0].mid,
    currentDirection: pc.getTransceivers()[0].currentDirection,
    direction: pc.getTransceivers()[0].direction,
  } : null;
  out.receiver = receiver && receiver.track ? {
    kind: receiver.track.kind, muted: receiver.track.muted,
    readyState: receiver.track.readyState, id: receiver.track.id,
  } : null;
  if (!video.srcObject && receiver && receiver.track) {
    video.srcObject = new MediaStream([receiver.track]);
    out.attachedManually = true;
    await video.play().then(() => log('video.play() started (attached manually)'),
                            (e) => log('manual play() rejected: ' + e));
  }
  log(`receiver: ${JSON.stringify(out.receiver)} transceiver: ${JSON.stringify(out.transceiver)} attachedManually=${!!out.attachedManually}`);

  // Geometry: the host located the strip in a screenshot; the video is the same
  // picture, so the offset is a scale of it. The first frames are used to verify
  // that, and to try small corrections, because a cell-aligned read is the whole
  // precondition for reading bits instead of noise.
  //
  // The cell comes from the host in the VIDEO's pixels: a downscaled stream has a
  // fractional cell there, and using the screen's 32 would sample the wrong cell
  // on every column.
  const cell = cfg.strip.videoCell > 0 ? cfg.strip.videoCell : cfg.strip.cell;
  const candidates = [];
  for (const dx of [0, -4, -2, 2, 4]) {
    for (const dy of [0, -4, -2, 2, 4]) {
      candidates.push({ x: cfg.strip.videoX + dx, y: cfg.strip.videoY + dy, cell, dx, dy, ok: 0, tried: 0 });
    }
  }
  let geom = candidates[0];
  let calibrated = false;
  let frames = 0;
  let recording = false;
  let state = null;

  const step = (now, meta) => {
    if (recording) {
      frames++;
      const t0 = performance.now();
      // Calibration window: try every candidate offset for the first frames and
      // keep the one that decodes most often.
      if (!calibrated && frames <= 40) {
        for (const c of candidates) {
          c.tried++;
          try {
            const r = decodeStrip(video, ctx, c);
            if (!r.err) c.ok++;
          } catch (e) { /* out of bounds while calibrating */ }
        }
      }
      const readStart = performance.now();
      let r = { err: 'not-read' };
      try {
        r = decodeStrip(video, ctx, geom);
      } catch (e) {
        r = { err: 'exception: ' + e };
      }
      const readMS = performance.now() - readStart;
      if (out.readMSMax === undefined || readMS > out.readMSMax) out.readMSMax = readMS;
      out.readMSSum = (out.readMSSum || 0) + readMS;
      out.readMSN = (out.readMSN || 0) + 1;

      const s = {
        cb: abs(now),
        edt: abs(meta.expectedDisplayTime),
        pf: meta.presentedFrames,
        pt: meta.presentationTime,
        mt: meta.mediaTime,
        proc: meta.processingDuration,
        w: meta.presentationWidth,
        h: meta.presentationHeight,
        readMS,
        ms: r.ms === undefined ? null : r.ms,
        react: r.reacting === true,
        taps: r.tapCount === undefined ? null : r.tapCount,
        err: r.err || null,
      };
      out.samples.push(s);
    }
    video.requestVideoFrameCallback(step);
  };
  video.requestVideoFrameCallback(step);

  // Wait for the host to start the pump, then record for the run plus a tail.
  const hardStop = performance.timeOrigin + performance.now() + 150000;
  for (let i = 0; i < 1200; i++) {
    state = await fetch('/state', { cache: 'no-store' }).then((r) => r.json());
    if (state.pumpStarted) break;
    await new Promise((r) => setTimeout(r, 100));
  }
  if (!state || !state.pumpStarted) {
    out.error = 'pump never started';
    await post(out);
    window.__spike.done = true;
    return;
  }
  out.startNS = state.startNS;
  recording = true;
  out.recordingStart = abs(performance.now());

  // Video-element state while recording: a stream that arrives and never renders
  // looks identical to an empty stream in every counter except these.
  const videoDiag = setInterval(() => {
    if (!recording) return;
    out.videoDiag = out.videoDiag || [];
    const entry = {
      t: performance.timeOrigin + performance.now(),
      paused: video.paused, readyState: video.readyState, muted: video.muted,
      videoWidth: video.videoWidth, videoHeight: video.videoHeight,
      currentTime: video.currentTime, hasSrcObject: !!video.srcObject,
      samples: out.samples.length,
    };
    out.videoDiag.push(entry);
    log(`video: paused=${entry.paused} readyState=${entry.readyState} muted=${entry.muted} ` +
        `size=${entry.videoWidth}x${entry.videoHeight} t=${entry.currentTime.toFixed(2)} ` +
        `srcObject=${entry.hasSrcObject} samples=${entry.samples}`);
    // The rig waits for this before it injects anything: a tap whose reaction
    // could never appear in the video is not a measurement.
    let decoded = 0;
    for (const s of out.samples) if (s.ms !== null) decoded++;
    fetch('/progress', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        samples: out.samples.length, decoded, paused: entry.paused,
        readyState: entry.readyState, videoWidth: entry.videoWidth,
      }),
    }).catch(() => {});
  }, 1000);
  out.stopVideoDiag = () => clearInterval(videoDiag);

  const statsTimer = setInterval(async () => {
    if (!recording) return;
    try {
      const snap = rtcpSnapshot(await pc.getStats());
      snap.wall = abs(performance.now());
      out.stats.push(snap);
    } catch (e) { out.errors.push('getStats: ' + e); }
  }, 500);

  while (performance.timeOrigin + performance.now() < hardStop) {
    state = await fetch('/state', { cache: 'no-store' }).then((r) => r.json()).catch(() => state);
    if (state && state.pumpFinished) break;
    await new Promise((r) => setTimeout(r, 200));
  }
  // A tail after the last frame so the final samples land.
  await new Promise((r) => setTimeout(r, 2500));
  recording = false;
  clearInterval(statsTimer);
  if (out.stopVideoDiag) out.stopVideoDiag();

  // Pick the best calibration candidate and report it.
  const tried = candidates.map((c) => ({ ...c, rate: c.tried ? c.ok / c.tried : 0 }));
  tried.sort((a, b) => b.rate - a.rate || Math.abs(a.dx) + Math.abs(a.dy) - (Math.abs(b.dx) + Math.abs(b.dy)));
  out.offsets = tried;
  out.geometry = { configured: cfg.strip, chosen: tried[0], changed: tried[0].dx !== 0 || tried[0].dy !== 0 };
  log(`geometry: configured (${cfg.strip.videoX},${cfg.strip.videoY}) cell ${cfg.strip.cell}; calibration best dx=${tried[0].dx} dy=${tried[0].dy} rate=${tried[0].rate.toFixed(3)}`);

  const byErr = {};
  let decoded = 0;
  for (const s of out.samples) {
    if (s.ms !== null) decoded++;
    else byErr[s.err] = (byErr[s.err] || 0) + 1;
  }
  out.decode = {
    frames: out.samples.length, decoded, failed: out.samples.length - decoded, byErr,
    rate: out.samples.length ? decoded / out.samples.length : 0,
    readMSMean: out.readMSN ? out.readMSSum / out.readMSN : null,
    readMSMax: out.readMSMax,
  };
  out.finalStats = rtcpSnapshot(await pc.getStats());
  const q = video.getVideoPlaybackQuality ? video.getVideoPlaybackQuality() : {};
  out.playbackQuality = {
    totalVideoFrames: q.totalVideoFrames, droppedVideoFrames: q.droppedVideoFrames,
    corruptedVideoFrames: q.corruptedVideoFrames, videoWidth: video.videoWidth,
    videoHeight: video.videoHeight, readyState: video.readyState,
  };
  await post(out);
  window.__spike.results = out;
  window.__spike.done = true;
  log('done');
}

async function post(out) {
  const body = JSON.stringify(out, (k, v) => (k === 'pc' || k === 'readPromise' ? undefined : v));
  try {
    await fetch(`/result?transport=${out.transport}`, {
      method: 'POST', headers: { 'content-type': 'application/json' }, body,
    });
    log(`posted results (${body.length} bytes)`);
  } catch (e) {
    log('posting results failed: ' + e);
  }
}

window.__spike = { done: false, results: null, log: () => logEl.textContent };

main().catch((e) => {
  log('fatal: ' + (e && e.stack ? e.stack : e));
  window.__spike.error = String(e);
  window.__spike.done = true;
});
