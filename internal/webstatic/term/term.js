// Bridge between xterm.js and the zlily TUI compiled to WebAssembly.
//
// The Go side (cmd/zlily-wasm) exposes a `zlily` global once it is running:
//
//   zlily.start({cols, rows, onOutput})   begin the program
//   zlily.write(data)                     keystrokes, verbatim
//   zlily.resize(cols, rows)              geometry, standing in for SIGWINCH
//
// This file owns the terminal; the Go side owns everything drawn in it.

// This page's build, stamped in by the server. Every asset is requested with
// it, so a rebuilt proxy serves new URLs rather than relying on the browser to
// notice that a cached 20 MB wasm is stale.
const BUILD = window.ZLILY_BUILD || '';

// Where this script was served from, which is the directory every other asset
// lives in. Resolving against it rather than against the page URL matters: a
// relative URL is resolved against the document, so on /term (no trailing
// slash) "zlily.wasm" would resolve to /zlily.wasm — one level too high, and
// served by a different handler. document.currentScript is only readable while
// the script is executing, hence reading it here.
const ASSET_BASE = new URL('.', document.currentScript.src);
const asset = (path) => new URL(path, ASSET_BASE).href;

// A build ID is twelve hex characters. Anything else is not an answer from the
// build endpoint, and must never be compared against as though it were: a
// mismatch would show the update banner on every check, for ever.
const BUILD_ID = /^[0-9a-f]{12}$/;

const boot = document.getElementById('boot');
const statusEl = document.getElementById('status');
const hintEl = document.getElementById('hint');
const progress = document.querySelector('#bar > i');

function fail(msg, detail) {
  statusEl.className = 'err';
  statusEl.textContent = msg;
  hintEl.textContent = detail || '';
  progress.style.width = '0';
}

const term = new Terminal({
  // Do NOT set convertEol. Bubble Tea emits bare \n on this path and the Go
  // side already restores the carriage returns (internal/tui/onlcr); turning
  // it on here as well would double them.
  convertEol: false,
  cursorBlink: false,
  // The TUI hides the real cursor and draws its own cell, so xterm's cursor
  // would be a second, wrong one.
  cursorStyle: 'bar',
  cursorInactiveStyle: 'none',
  // Send Option+key as ESC-prefixed Meta rather than letting macOS compose a
  // character from it. The TUI's keymap is Emacs-shaped and leans on Meta
  // heavily (M-b, M-f, M-t, M-v, M-x, M-s, …), so Meta wins the key.
  //
  // The cost is that Option no longer composes accented characters — Option+e
  // then e stops producing e-acute. Paste still works, and flipping this off
  // brings composition back at the price of every M- binding.
  macOptionIsMeta: true,
  // Option-click is how macOS terminals let you select text past a mouse-mode
  // application; keep that working rather than routing it to the app.
  macOptionClickForcesSelection: true,
  fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
  fontSize: 14,
  scrollback: 0, // the TUI owns scrollback; xterm keeping its own fights it
  allowProposedApi: true,
  theme: {
    background: '#16161a',
    foreground: '#d5d5da',
    cursor: '#d5d5da',
    selectionBackground: '#33415e',
  },
  // OSC 8 hyperlinks: the TUI emits them (internal/tui/ui/links.go) whenever
  // TERM does not look like screen, which in wasm it never does.
  linkHandler: {
    activate(_event, uri) {
      window.open(uri, '_blank', 'noopener,noreferrer');
    },
  },
});

const fit = new FitAddon.FitAddon();
term.loadAddon(fit);

// Keys the browser would otherwise steal. C-w closes the tab and C-r reloads;
// both are load-bearing in the TUI (kill-word and reverse history search).
// C-n and C-t cannot be intercepted in some browsers — that is a real gap, so
// it is stated on screen rather than papered over.
const RECLAIMED = new Set(['w', 'r']);
term.attachCustomKeyEventHandler((e) => {
  if (e.type === 'keydown' && e.ctrlKey && !e.altKey && !e.metaKey && RECLAIMED.has(e.key)) {
    e.preventDefault();
  }
  return true;
});

// Session token storage.
//
// The proxy session outlives this page: a reload, or a tab the browser
// discarded overnight, ends the program while the session it was driving is
// still live on the proxy. Keeping the token lets the next load re-attach
// instead of asking for a password the proxy would only use to hand back that
// same session.
//
// The token is a bearer credential for a live Lily session, so where it goes
// matters. localStorage keeps it to this origin and out of every request the
// browser makes on its own — which is what keeps the proxy's bearer-token
// scheme immune to CSRF; a cookie would be attached automatically and would
// need its own defence. It is still readable by script on this origin, so it is
// only as safe as this page: treat it like a logged-in session, because that is
// what it is.
const TOKEN_KEY = 'zlily.session.token';

function loadToken() {
  try {
    return localStorage.getItem(TOKEN_KEY) || '';
  } catch (e) {
    // Private windows and "block site data" make localStorage throw rather than
    // return null. Losing the token only costs a login.
    return '';
  }
}

// The Go side calls this after every successful login or reconnect, and with
// null when the proxy rejects a token.
globalThis.zlilySaveToken = (token) => {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token);
    else localStorage.removeItem(TOKEN_KEY);
  } catch (e) {
    /* not fatal: the next load just starts at the login prompt */
  }
};

// The Go side calls this for `%debug snapshot` (M-x), which on a real terminal
// writes a file. The snapshot quotes the input line and recent keystrokes, so
// it stays on the user's machine: this hands it to the browser's download
// mechanism and never sends it anywhere.
globalThis.zlilySaveFile = (filename, text) => {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
};

// Watching for a newer build.
//
// The assets a *reload* fetches are always current, but a tab left open for
// days keeps running whatever it loaded. This notices and offers to reload,
// rather than reloading on its own: a reload is cheap now that the session
// survives it (see zlilySaveToken above), but it would still discard a
// half-typed message, and a mismatch is usually harmless — the API is the
// same shape across most builds.
const UPDATE_POLL_MS = 5 * 60 * 1000;
let updateShown = false;

async function checkForUpdate() {
  if (updateShown || !BUILD) return;
  try {
    const resp = await fetch(asset('build'), { cache: 'no-store' });
    if (!resp.ok) return;
    const latest = (await resp.text()).trim();
    if (!BUILD_ID.test(latest)) return; // not the build endpoint answering
    if (latest !== BUILD) {
      console.info(`zlily: this page is build ${BUILD}, the server is serving ${latest}`);
      showUpdateBanner(latest);
    }
  } catch (e) {
    // Offline, or the proxy is restarting — the next poll will tell us.
  }
}

function showUpdateBanner(latest) {
  updateShown = true;
  const el = document.getElementById('update');
  if (!el) return; // an older page that predates the banner markup
  const label = el.querySelector('span');
  if (label) label.textContent = `A newer zlily is available (${latest.slice(0, 7)}).`;
  document.getElementById('update-reload').onclick = reloadToLatest;
  document.getElementById('update-dismiss').onclick = () => el.classList.remove('show');
  el.classList.add('show');
}

// location.reload() honours the HTTP cache, which is not good enough here: the
// whole reason for the banner is that this page is not the current one, and a
// copy of index.html cached before it carried cache headers would simply be
// served again. Refetching it with cache:'reload' bypasses and replaces the
// cached entry first, so the reload that follows genuinely lands on the new
// build.
async function reloadToLatest() {
  try {
    await fetch(location.href, { cache: 'reload' });
  } catch (e) {
    // Not fatal; the reload below may still pick it up.
  }
  location.reload();
}

function watchForUpdates() {
  // Check straight away, not only after the first interval. Without this a
  // reload appears to do nothing — the banner is gone because no check has run
  // yet — and then reappears minutes later, which reads as the button being
  // broken rather than as the page being out of date.
  checkForUpdate();
  setInterval(checkForUpdate, UPDATE_POLL_MS);
  // Coming back to a tab is exactly when a days-old build is likely, and is
  // the moment the answer is most useful.
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) checkForUpdate();
  });
}

async function main() {
  const termEl = document.getElementById('term');
  term.open(termEl);

  // Draw with WebGL rather than the DOM.
  //
  // This is not about speed. The DOM renderer emits block and box-drawing
  // characters as text, so what you get is whatever the font has: a U+2588
  // FULL BLOCK that stops short of the cell leaves a horizontal gap between
  // rows, and a U+2580 UPPER HALF BLOCK whose split is off by a fraction of a
  // pixel leaves a seam. The splash art is made of exactly those characters,
  // and so are the dialog borders. The WebGL renderer draws them from geometry
  // instead, so they meet exactly.
  //
  // It has to come after open(), and it is optional: a browser with no WebGL,
  // or one that loses the context, falls back to the DOM renderer, which is
  // correct but grainier for block art.
  try {
    const webgl = new WebglAddon.WebglAddon();
    webgl.onContextLoss(() => webgl.dispose());
    term.loadAddon(webgl);
  } catch (e) {
    console.info('zlily: WebGL renderer unavailable, falling back to the DOM renderer', e);
  }

  fit.fit();

  if (!WebAssembly.instantiateStreaming) {
    fail('This browser cannot stream WebAssembly.', 'Chrome, Firefox, Safari 15+ or Edge are needed.');
    return;
  }

  // One Go instance throughout: the module must be instantiated against the
  // same importObject that later runs it.
  const go = new Go();

  let wasm;
  try {
    wasm = await fetchWasm(asset('zlily.wasm?v=' + encodeURIComponent(BUILD)), go);
  } catch (err) {
    fail('Could not load zlily.wasm.', String(err));
    return;
  }

  statusEl.textContent = 'Starting…';

  // main() parks forever, so go.run() never resolves. The Go side calls
  // zlilyOnReady as soon as the bridge globals exist.
  const ready = new Promise((resolve) => { globalThis.zlilyOnReady = resolve; });
  go.run(wasm.instance).catch((err) => fail('zlily stopped.', String(err)));
  await ready;

  boot.classList.add('done');
  termEl.classList.remove('hidden');
  fit.fit();

  zlily.start({
    cols: term.cols,
    rows: term.rows,
    // Empty when there is nothing stored; the Go side then shows the login
    // dialog exactly as it did before.
    token: loadToken(),
    // Bytes, not a string: term.write accepts a Uint8Array and decodes UTF-8
    // with state carried across chunks, which matters because the renderer can
    // split a multi-byte glyph across two writes.
    onOutput: (bytes) => term.write(bytes),
  });

  term.onData((data) => zlily.write(data));
  term.focus();

  // Report geometry on any layout change: window resize, device rotation, or
  // a mobile soft keyboard taking half the viewport.
  const report = () => {
    fit.fit();
    zlily.resize(term.cols, term.rows);
  };
  new ResizeObserver(debounce(report, 60)).observe(termEl);
  window.addEventListener('orientationchange', () => setTimeout(report, 120));

  watchForUpdates();
}

// fetchWasm streams the module while reporting download progress — the binary
// is ~19 MB uncompressed, so a blank screen would otherwise look like a hang.
async function fetchWasm(url, go) {
  const resp = await fetch(url);
  if (!resp.ok) throw new Error(`${resp.status} ${resp.statusText}`);

  const total = Number(resp.headers.get('content-length') || 0);
  // With Content-Encoding set, content-length describes the compressed size
  // while the body reads decompressed, so progress is only meaningful when the
  // response is identity-encoded. Fall back to an indeterminate bar otherwise.
  if (!total || !resp.body) {
    progress.style.width = '100%';
    statusEl.textContent = 'Loading zlily…';
    return WebAssembly.instantiateStreaming(resp, go.importObject);
  }

  let loaded = 0;
  const stream = new ReadableStream({
    start(controller) {
      const reader = resp.body.getReader();
      (function pump() {
        reader.read().then(({ done, value }) => {
          if (done) { controller.close(); return; }
          loaded += value.byteLength;
          const pct = Math.min(100, (loaded / total) * 100);
          progress.style.width = pct + '%';
          statusEl.textContent = `Loading zlily… ${Math.round(pct)}%`;
          controller.enqueue(value);
          pump();
        }).catch((e) => controller.error(e));
      })();
    },
  });

  const piped = new Response(stream, { headers: { 'content-type': 'application/wasm' } });
  return WebAssembly.instantiateStreaming(piped, go.importObject);
}

function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

main();
