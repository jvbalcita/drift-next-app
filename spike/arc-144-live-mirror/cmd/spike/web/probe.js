// Browser half of the ARC-144 latency spike.
//
// It renders the same H.264 source over two transports at once (an RTP track
// from pion/webrtc and a fragmented MP4 into Media Source Extensions) and,
// for every frame the compositor presents, reads the frame's own index back
// out of its pixels (the barcode burned in by cmd/genframes) and records the
// browser's expected display time for that frame.
//
// That gives one number per frame per transport -- when the source frame was
// released by the Go hop, and when the browser expected to display the frame
// carrying that index -- with no clock shared between the two beyond the
// machine clock both processes are already reading.
'use strict';

const BARCODE = {
  originX: 48,
  originY: 48,
  cell: 32,
  indexBits: 16,
  totalBits: 20,
  sync: [1, 0, 1, 0],
};

const logEl = document.getElementById('log');
function log(msg) {
  const line = `${((performance.timeOrigin + performance.now()) / 1000).toFixed(3)} ${msg}`;
  logEl.textContent += line + '\n';
  console.log('[spike] ' + msg);
}

// reader() reads the frame index out of the pixels of the frame the element is
// currently presenting.
//
// The barcode strip (20 cells of 32x32 at (48,48)) is drawn into a 20x1 canvas:
// one output pixel per cell, averaged by the downscale, and only 80 bytes are
// read back. An earlier version drew the strip 1:1 and read 20k pixels per
// frame; that per-frame readback stall was itself causing dropped frames, which
// would have contaminated the measurement it was taking.
//
// Returns null when the sync cells do not validate, which marks a torn or
// partially presented frame rather than a plausible wrong index.
function makeReader(video) {
  const w = BARCODE.totalBits;
  const canvas = document.createElement('canvas');
  canvas.width = w;
  canvas.height = 1;
  const ctx = canvas.getContext('2d', { willReadFrequently: true, alpha: false });
  ctx.imageSmoothingEnabled = true;

  return function read() {
    ctx.drawImage(
      video,
      BARCODE.originX, BARCODE.originY, BARCODE.totalBits * BARCODE.cell, BARCODE.cell,
      0, 0, w, 1,
    );
    const d = ctx.getImageData(0, 0, w, 1).data;
    let index = 0;
    for (let i = 0; i < BARCODE.totalBits; i++) {
      const bit = d[i * 4] >= 128 ? 1 : 0;
      if (i < BARCODE.indexBits) {
        index |= bit << i;
      } else if (bit !== BARCODE.sync[i - BARCODE.indexBits]) {
        return null;
      }
    }
    return index;
  };
}

// sampleVideo records one entry per presented frame while the recording flag
// is set. Each entry carries the frame index read from the pixels and the
// browser's own estimate of when that frame is displayed.
function sampleVideo(video, out, readbackEvery) {
  const reader = readbackEvery > 0 ? makeReader(video) : null;
  let seen = 0;
  const step = (now, meta) => {
    if (out.recording) {
      const doRead = reader && (readbackEvery === 1 || seen % readbackEvery === 0);
      seen++;
      let index = null;
      let readError = null;
      const t0 = performance.now();
      try {
        if (doRead) index = reader();
      } catch (e) {
        readError = String(e);
      }
      const readMS = doRead ? performance.now() - t0 : 0;
      out.samples.push({
        i: index,
        edt: performance.timeOrigin + meta.expectedDisplayTime,
        cb: performance.timeOrigin + now,
        pf: meta.presentedFrames,
        pt: meta.presentationTime,
        mt: meta.mediaTime,
        proc: meta.processingDuration,
        w: meta.presentationWidth,
        h: meta.presentationHeight,
        readMS,
        err: readError,
      });
      if (out.readMSMax === undefined || readMS > out.readMSMax) out.readMSMax = readMS;
    }
    video.requestVideoFrameCallback(step);
  };
  video.requestVideoFrameCallback(step);
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

// clockOffset estimates how far this page's clock is from the server's, in ms,
// using the lowest-RTT NTP-style sample. On one machine this should be ~0, and
// reporting it is what makes the join between the two logs defensible.
async function clockOffset(samples = 25) {
  const out = [];
  for (let i = 0; i < samples; i++) {
    const t0 = performance.timeOrigin + performance.now();
    const r = await fetch('/now', { cache: 'no-store' }).then((x) => x.json());
    const t1 = performance.timeOrigin + performance.now();
    out.push({ rtt: t1 - t0, offset: r.ns / 1e6 - (t0 + t1) / 2 });
  }
  out.sort((a, b) => a.rtt - b.rtt);
  return {
    samples: out.length,
    bestRTT: out[0].rtt,
    offsetMS: out[0].offset,
    minOffsetMS: Math.min(...out.map((o) => o.offset)),
    maxOffsetMS: Math.max(...out.map((o) => o.offset)),
  };
}

async function capabilities(cfg) {
  const caps = {
    userAgent: navigator.userAgent,
    videoElementDimensions: [document.getElementById('rtpVideo').videoWidth,
      document.getElementById('rtpVideo').videoHeight],
  };
  const videoInfo = (contentType, type) => ({
    contentType,
    width: 1280,
    height: 720,
    bitrate: 4_000_000,
    framerate: cfg.fps,
  });
  try {
    caps.mseIsTypeSupported = MediaSource.isTypeSupported(cfg.mseContentType);
  } catch (e) {
    caps.mseIsTypeSupported = 'error: ' + e;
  }
  if (navigator.mediaCapabilities) {
    try {
      caps.decodingInfoWebRTC = await navigator.mediaCapabilities.decodingInfo({
        type: 'webrtc',
        video: videoInfo('video/H264'),
      });
    } catch (e) {
      caps.decodingInfoWebRTC = 'error: ' + e;
    }
    try {
      caps.decodingInfoMediaSource = await navigator.mediaCapabilities.decodingInfo({
        type: 'media-source',
        video: videoInfo(cfg.mseContentType),
      });
    } catch (e) {
      caps.decodingInfoMediaSource = 'error: ' + e;
    }
  }
  if (typeof VideoDecoder !== 'undefined') {
    const codec = 'avc1.' + cfg.profileLevelID;
    try {
      caps.videoDecoder = await VideoDecoder.isConfigSupported({
        codec, codedWidth: 1280, codedHeight: 720,
      });
    } catch (e) {
      caps.videoDecoder = 'error: ' + e;
    }
    try {
      caps.videoDecoderPreferHardware = await VideoDecoder.isConfigSupported({
        codec, codedWidth: 1280, codedHeight: 720, hardwareAcceleration: 'prefer-hardware',
      });
    } catch (e) {
      caps.videoDecoderPreferHardware = 'error: ' + e;
    }
  }
  return caps;
}

function rtcpSnapshot(report) {
  const out = {};
  report.forEach((s) => {
    if (s.type === 'inbound-rtp' && (s.kind === 'video' || s.mediaType === 'video')) {
      out.inbound = {
        codec: s.codecId,
        framesReceived: s.framesReceived,
        framesDecoded: s.framesDecoded,
        framesDropped: s.framesDropped,
        keyFramesDecoded: s.keyFramesDecoded,
        freezeCount: s.freezeCount,
        totalFreezesDuration: s.totalFreezesDuration,
        packetsReceived: s.packetsReceived,
        packetsLost: s.packetsLost,
        bytesReceived: s.bytesReceived,
        jitter: s.jitter,
        jitterBufferDelay: s.jitterBufferDelay,
        jitterBufferEmittedCount: s.jitterBufferEmittedCount,
        totalDecodeTime: s.totalDecodeTime,
        totalProcessingDelay: s.totalProcessingDelay,
        totalAssemblyTime: s.totalAssemblyTime,
        totalInterFrameDelay: s.totalInterFrameDelay,
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

async function setupRTP(cfg, caps) {
  const video = document.getElementById('rtpVideo');
  const out = {
    transport: 'rtp',
    ready: false,
    recording: false,
    connected: false,
    samples: [],
    stats: [],
    errors: [],
    caps,
  };
  sampleVideo(video, out, cfg.readbackEvery);

  const pc = new RTCPeerConnection({ iceServers: [] });
  out.pc = pc;
  const decoder = new Promise((resolve) => {
    pc.ontrack = (ev) => {
      video.srcObject = ev.streams[0];
      video.play().then(
        () => log('rtp: video.play() started'),
        (e) => { out.errors.push('play: ' + e); log('rtp: play() rejected: ' + e); });
      resolve();
    };
  });
  pc.addTransceiver('video', { direction: 'recvonly' });

  const connected = new Promise((resolve) => {
    pc.onconnectionstatechange = () => {
      out.connectionState = pc.connectionState;
      log('rtp: connection state ' + pc.connectionState);
      if (pc.connectionState === 'connected') resolve();
    };
  });

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  await iceComplete(pc);
  const answer = await fetch('/offer', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ sdp: pc.localDescription.sdp }),
  }).then((r) => r.json());
  // Keep the negotiated answer: it is the only place the fmtp actually agreed
  // for this connection is recorded.
  out.answerSDP = answer.sdp;
  await pc.setRemoteDescription({ type: 'answer', sdp: answer.sdp });
  await Promise.race([connected, new Promise((r) => setTimeout(r, 10000))]);
  await Promise.race([decoder, new Promise((r) => setTimeout(r, 5000))]);

  const receivers = pc.getReceivers();
  if (receivers[0]) {
    out.receiverParameters = receivers[0].getParameters();
  }
  out.notified = true;
  return out;
}

function appendBuffer(sb, bytes) {
  return new Promise((resolve, reject) => {
    sb.onupdateend = null;
    sb.onerror = null;
    sb.onupdateend = () => resolve();
    sb.onerror = (e) => reject(new Error('sourcebuffer: ' + (e && e.message)));
    try {
      sb.appendBuffer(bytes);
    } catch (e) {
      reject(e);
    }
  });
}

async function setupMSE(cfg, caps) {
  const video = document.getElementById('mseVideo');
  const out = {
    transport: 'mse',
    ready: false,
    recording: false,
    samples: [],
    stats: [],
    errors: [],
    fragmentArrivals: [],
    caps,
  };
  sampleVideo(video, out, cfg.readbackEvery);

  const ms = new MediaSource();
  video.src = URL.createObjectURL(ms);
  await new Promise((resolve) => ms.addEventListener('sourceopen', resolve, { once: true }));
  out.mediaSourceReadyState = ms.readyState;

  let sb;
  try {
    sb = ms.addSourceBuffer(cfg.mseContentType);
  } catch (e) {
    out.errors.push('addSourceBuffer: ' + e);
    throw e;
  }
  out.sourceBuffer = { type: cfg.mseContentType, mode: sb.mode };

  const init = new Uint8Array(await fetch('/mse/init', { cache: 'no-store' }).then((r) => r.arrayBuffer()));
  await appendBuffer(sb, init);
  log(`mse: appended init segment (${init.byteLength} bytes), buffered=${sb.buffered.length ? sb.buffered.end(0) : 0}`);

  video.play().then(
    () => log('mse: video.play() started'),
    (e) => { out.errors.push('play: ' + e); log('mse: play() rejected: ' + e); });

  // Read the length-prefixed fragment stream. Each fragment is appended the
  // moment it arrives: this is a straight MSE pipeline with no attempt to hide
  // fragment latency, which is the honest baseline the RTP path is compared to.
  const resp = await fetch('/mse/stream', { cache: 'no-store' });
  out.ready = true;
  const reader = resp.body.getReader();
  let pending = new Uint8Array(0);
  const readAll = async () => {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      const merged = new Uint8Array(pending.length + value.length);
      merged.set(pending, 0);
      merged.set(value, pending.length);
      pending = merged;
      while (pending.length >= 4) {
        const len = new DataView(pending.buffer, pending.byteOffset, 4).getUint32(0);
        if (pending.length < 4 + len) break;
        const frag = pending.slice(4, 4 + len);
        pending = pending.slice(4 + len);
        out.fragmentArrivals.push({
          t: performance.timeOrigin + performance.now(),
          bytes: len,
        });
        await appendBuffer(sb, frag).catch((e) => out.errors.push(String(e)));
        if (out.recording) {
          // Keep playback at the live edge, the way any low-latency MSE player
          // has to: without this the element plays out the buffer it has and
          // the measured latency drifts upward for ever.
          const end = sb.buffered.length ? sb.buffered.end(sb.buffered.length - 1) : 0;
          if (end - video.currentTime > 0.25) {
            video.currentTime = end - 0.1;
            out.seeks = (out.seeks || 0) + 1;
          }
        }
      }
    }
    out.streamEnded = true;
    log('mse: fragment stream ended');
  };
  out.readPromise = readAll();
  return out;
}

async function postResult(out) {
  const body = JSON.stringify(out, (k, v) => (k === 'pc' || k === 'readPromise' ? undefined : v));
  try {
    await fetch(`/result?transport=${out.transport}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body,
    });
    log(`${out.transport}: posted results (${body.length} bytes)`);
  } catch (e) {
    log(`${out.transport}: posting results failed: ${e}`);
  }
}

async function main() {
  const cfg = await fetch('/config', { cache: 'no-store' }).then((r) => r.json());
  log(`config: ${JSON.stringify(cfg)}`);
  const offset = await clockOffset();
  log(`clock offset: ${offset.offsetMS.toFixed(3)} ms (best RTT ${offset.bestRTT.toFixed(3)} ms)`);
  const caps = await capabilities(cfg);
  log(`caps: mseAvc=${caps.mseIsTypeSupported} ` +
      `webrtc=${JSON.stringify(caps.decodingInfoWebRTC && caps.decodingInfoWebRTC.supported)} ` +
      `ms=${JSON.stringify(caps.decodingInfoMediaSource && caps.decodingInfoMediaSource.supported)}`);

  const results = { cfg, offset, caps };
  const rtp = await setupRTP(cfg, caps);
  const mse = await setupMSE(cfg, caps);
  results.rtp = rtp;
  results.mse = mse;

  await fetch('/ready', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ transport: 'rtp', info: rtp.receiverParameters || null }),
  });
  await fetch('/ready', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ transport: 'mse', info: mse.sourceBuffer }),
  });
  log('both consumers ready; waiting for the pacer');

  // Wait for the server to start the run, then record for the run's duration
  // plus a tail.
  let state = null;
  for (let i = 0; i < 600; i++) {
    state = await fetch('/state', { cache: 'no-store' }).then((r) => r.json());
    if (state.started) break;
    await new Promise((r) => setTimeout(r, 100));
  }
  if (!state || !state.started) {
    log('pacer never started');
    results.error = 'pacer never started';
    window.__spike.done = true;
    return;
  }
  results.startNS = state.startNS;
  results.recordingStart = performance.timeOrigin + performance.now();
  rtp.recording = true;
  mse.recording = true;

  // Per-frame stats sampling for the RTP path while it runs.
  const statsTimer = setInterval(async () => {
    if (!rtp.recording) return;
    try {
      const report = await rtp.pc.getStats();
      const snap = rtcpSnapshot(report);
      snap.wall = performance.timeOrigin + performance.now();
      rtp.stats.push(snap);
    } catch (e) {
      rtp.errors.push('getStats: ' + e);
    }
  }, 500);

  const runEnds = state.startNS / 1e6 + cfg.runSeconds * 1000 + 2500;
  while (performance.timeOrigin + performance.now() < runEnds) {
    await new Promise((r) => setTimeout(r, 200));
  }
  rtp.recording = false;
  mse.recording = false;
  clearInterval(statsTimer);

  try {
    const report = await rtp.pc.getStats();
    rtp.finalStats = rtcpSnapshot(report);
  } catch (e) {
    rtp.errors.push('final getStats: ' + e);
  }
  rtp.playbackQuality = videoQuality(document.getElementById('rtpVideo'));
  mse.playbackQuality = videoQuality(document.getElementById('mseVideo'));
  mse.readyState = document.getElementById('mseVideo').readyState;
  mse.buffered = bufferedRanges(document.getElementById('mseVideo'));

  results.offsetAfter = await clockOffset(10);
  await postResult(rtp);
  await postResult(mse);
  results.done = true;
  window.__spike.results = results;
  window.__spike.done = true;
  log('done');
}

function videoQuality(video) {
  const q = video.getVideoPlaybackQuality ? video.getVideoPlaybackQuality() : {};
  return {
    totalVideoFrames: q.totalVideoFrames,
    droppedVideoFrames: q.droppedVideoFrames,
    corruptedVideoFrames: q.corruptedVideoFrames,
    totalFrameDelay: q.totalFrameDelay,
    videoWidth: video.videoWidth,
    videoHeight: video.videoHeight,
    readyState: video.readyState,
    currentTime: video.currentTime,
  };
}

function bufferedRanges(video) {
  const out = [];
  for (let i = 0; i < video.buffered.length; i++) {
    out.push([video.buffered.start(i), video.buffered.end(i)]);
  }
  return out;
}

window.__spike = {
  done: false,
  results: null,
  log: () => logEl.textContent,
};

main().catch((e) => {
  log('fatal: ' + (e && e.stack ? e.stack : e));
  window.__spike.error = String(e);
  window.__spike.done = true;
});
