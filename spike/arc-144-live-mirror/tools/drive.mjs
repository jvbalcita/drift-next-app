// CDP driver for the ARC-144 spike.
//
// Launches Chrome on this machine (headless, in a throwaway profile), points it
// at the spike page, waits for the page to finish both measurements, and writes
// the page's collected results plus its console log to a JSON file.
//
// Usage: node drive.mjs <url> <out.json> [timeoutSeconds] [--headed]
//
// No dependencies: Node's built-in WebSocket and fetch talk to Chrome's
// DevTools endpoint directly.
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const url = process.argv[2] || 'http://127.0.0.1:8791/';
const outPath = process.argv[3] || 'results/page.json';
const timeoutS = Number(process.argv[4] || 180);
const headed = process.argv.includes('--headed');

const CHROME = process.env.CHROME_PATH ||
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'arc144-chrome-'));

const args = [
  `--user-data-dir=${profile}`,
  '--remote-debugging-port=0',
  '--no-first-run',
  '--no-default-browser-check',
  '--disable-extensions',
  '--disable-sync',
  '--hide-scrollbars',
  '--mute-audio',
  '--autoplay-policy=no-user-gesture-required',
  '--disable-background-timer-throttling',
  '--disable-renderer-backgrounding',
  '--disable-backgrounding-occluded-windows',
  '--disable-features=CalculateNativeWinOcclusion',
  '--window-size=1400,900',
  'about:blank',
];
if (!headed) args.unshift('--headless=new');

const chrome = spawn(CHROME, args, { stdio: ['ignore', 'pipe', 'pipe'] });
let chromeStderr = '';
chrome.stderr.on('data', (b) => { chromeStderr += b.toString(); });

function cleanup(code) {
  try { chrome.kill('SIGKILL'); } catch {}
  try { fs.rmSync(profile, { recursive: true, force: true }); } catch {}
  process.exit(code);
}

const portFile = path.join(profile, 'DevToolsActivePort');
const deadline = Date.now() + 30000;
while (!fs.existsSync(portFile)) {
  if (Date.now() > deadline) {
    console.error('chrome did not start; stderr:\n' + chromeStderr);
    cleanup(1);
  }
  await new Promise((r) => setTimeout(r, 100));
}
const portLines = fs.readFileSync(portFile, 'utf8').split('\n');
const [port] = portLines;
// The second line is the browser endpoint path, which includes a per-launch
// GUID; connecting without it is rejected.
const browserPath = (portLines[1] || '/devtools/browser').trim();

let nextId = 1;
const pending = new Map();
const consoleLines = [];
const exceptions = [];

const ws = new WebSocket(`ws://127.0.0.1:${port}${browserPath}`);
await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true });
  ws.addEventListener('error', (e) => reject(new Error('ws error: ' + e.message)), { once: true });
});

ws.addEventListener('message', (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) reject(new Error(JSON.stringify(msg.error)));
    else resolve(msg.result);
    return;
  }
  if (msg.method === 'Runtime.consoleAPICalled') {
    const text = (msg.params.args || []).map((a) => a.value ?? a.description ?? '').join(' ');
    consoleLines.push(`${msg.params.type}: ${text}`);
  }
  if (msg.method === 'Runtime.exceptionThrown') {
    const d = msg.params.exceptionDetails;
    exceptions.push(d.exception?.description || d.text);
  }
});

function send(method, params = {}, sessionId) {
  const id = nextId++;
  const payload = { id, method, params };
  if (sessionId) payload.sessionId = sessionId;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify(payload));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout waiting for ${method}`));
      }
    }, 30000);
  });
}

const { targetId } = await send('Target.createTarget', { url });
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
await send('Runtime.enable', {}, sessionId);
await send('Page.enable', {}, sessionId);
await send('Log.enable', {}, sessionId);

async function evaluate(expression, awaitPromise = false) {
  const res = await send('Runtime.evaluate', {
    expression,
    awaitPromise,
    returnByValue: true,
    userGesture: true,
  }, sessionId);
  if (res.exceptionDetails) {
    throw new Error(res.exceptionDetails.exception?.description || res.exceptionDetails.text);
  }
  return res.result.value;
}

const finishBy = Date.now() + timeoutS * 1000;
let done = false;
let progress = [];
while (Date.now() < finishBy) {
  try {
    const state = await evaluate('JSON.stringify({done: !!window.__spike?.done, error: window.__spike?.error || null, lines: document.getElementById("log")?.textContent?.split("\\n").length || 0})');
    const s = JSON.parse(state);
    progress = s;
    if (s.done) { done = true; break; }
  } catch (e) {
    // page may still be loading
  }
  await new Promise((r) => setTimeout(r, 1000));
}

let results = null;
let pageLog = '';
try {
  pageLog = await evaluate('document.getElementById("log")?.textContent || ""');
  results = JSON.parse(await evaluate('JSON.stringify(window.__spike?.results || null)'));
} catch (e) {
  exceptions.push('collecting results: ' + e.message);
}

const payload = {
  url,
  finished: done,
  progress,
  collectedAt: new Date().toISOString(),
  console: consoleLines,
  exceptions,
  pageLog,
  results,
};
fs.mkdirSync(path.dirname(path.resolve(outPath)), { recursive: true });
fs.writeFileSync(outPath, JSON.stringify(payload, null, 2));
console.log(`driver: done=${done} wrote ${outPath} (${JSON.stringify(payload).length} bytes)`);
if (!done) {
  console.log('driver: page log tail:\n' + pageLog.split('\n').slice(-25).join('\n'));
  console.log('driver: exceptions: ' + JSON.stringify(exceptions));
}
cleanup(done ? 0 : 2);
