// Drives a zlily.wasm build the way internal/webstatic/term/term.js does, so
// the browser client can be tested without a browser.
//
//   node browser_harness.cjs <zlily.wasm> <script.json> <out.bin> <result.json>
//
// The script names the proxy, an optional stored token, the terminal size, and
// a list of steps. Raw renderer output is written to out.bin for the Go side to
// replay through a terminal emulator; what the page's callbacks saw goes to
// result.json.
//
// Everything here mirrors term.js deliberately. If the two drift, this stops
// testing what actually ships.

const fs = require('fs');
const script = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

// Go disables the Fetch API when it recognises Node, routing HTTP over an
// in-process fake network that reaches nothing — see jsFetchDisabled in
// net/http/roundtrip_js.go and go.dev/issue/57613. Without this the client
// cannot talk to the proxy at all, so present a process object that does not
// look like Node. It has to be a plain copy: argv0 on the real process is
// non-configurable, which defeats both defineProperty and a Proxy.
const realProcess = process;
const fakeProcess = {};
for (const k in realProcess) {
  const v = realProcess[k];
  fakeProcess[k] = typeof v === 'function' ? v.bind(realProcess) : v;
}
fakeProcess.argv0 = 'not-node';
globalThis.process = fakeProcess;

globalThis.require = require;
globalThis.fs = fs;
globalThis.path = require('path');
globalThis.TextEncoder = require('util').TextEncoder;
globalThis.TextDecoder = require('util').TextDecoder;
globalThis.performance ??= require('perf_hooks').performance;
globalThis.crypto ??= require('crypto');

require(process.env.WASM_EXEC);

// What the page would have done with the token, recorded rather than stored.
const result = { saved: [], cleared: 0, error: null };
globalThis.zlilySaveToken = (t) => {
  if (t) result.saved.push(t);
  else result.cleared++;
};
globalThis.zlilySaveFile = (name, text) => {
  result.savedFile = { name, bytes: text.length };
};

const chunks = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Answer the terminal queries a real terminal would.
//
// Bubble Tea asks the terminal what colour it is (OSC 11) and recolours the
// splash from the reply. xterm.js answers by way of its data event, which the
// page pipes straight back into the client, so a harness that stays silent is
// not standing in for a browser — it is standing in for a terminal that never
// replies. That is a valid case too, but it is not the common one.
const OSC11_QUERY = /\x1b\]11;\?(\x07|\x1b\\)/;

function answerQueries(text) {
  if (!script.background || !OSC11_QUERY.test(text)) return;
  const hex = script.background.replace('#', '');
  const dup = (i) => hex.slice(i * 2, i * 2 + 2).repeat(2); // 8-bit -> 16-bit
  zlily.write(`\x1b]11;rgb:${dup(0)}/${dup(1)}/${dup(2)}\x1b\\`);
  result.answeredBackground = true;
}

function finish(code) {
  try {
    fs.writeFileSync(process.argv[4], Buffer.concat(chunks));
    fs.writeFileSync(process.argv[5], JSON.stringify(result));
  } catch (e) {
    // Nothing useful left to do; the Go side reports the missing file.
  }
  realProcess.exit(code);
}

(async () => {
  const go = new Go();
  const { instance } = await WebAssembly.instantiate(fs.readFileSync(process.argv[2]), go.importObject);

  const ready = new Promise((r) => { globalThis.zlilyOnReady = r; });
  go.run(instance).catch((e) => {
    // A Go panic reaches here. Record it: a crashed client that still drew a
    // plausible screen would otherwise look like a pass.
    result.error = String(e && e.stack ? e.stack : e);
  });
  await ready;

  zlily.start({
    cols: script.cols,
    rows: script.rows,
    proxy: script.proxy,
    token: script.token || '',
    onOutput: (bytes) => {
      const buf = Buffer.from(bytes);
      chunks.push(buf);
      answerQueries(buf.toString('latin1'));
    },
  });

  for (const step of script.steps) {
    if (result.error) break; // stop feeding a program that has already died
    if (step.wait) await sleep(step.wait);
    if (step.write !== undefined) {
      for (const ch of step.write) {
        zlily.write(ch);
        await sleep(step.perKey || 25);
      }
    }
    if (step.resize) zlily.resize(step.resize[0], step.resize[1]);
  }

  finish(0);
})().catch((e) => {
  result.error = String(e && e.stack ? e.stack : e);
  finish(1);
});
