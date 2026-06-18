<script>
  import { auth, fetchState, fetchEvents } from '../lib/api.js';
  import { setToken } from '../lib/api.js';
  import { token, whoami, entities, lastSeenID, serverPrompt } from '../lib/store.js';
  import { connect, send, disconnect } from '../lib/ws.js';
  import { processMessage } from '../lib/events.js';

  let username = $state('');
  let password = $state('');
  let error    = $state('');
  let loading  = $state(false);

  /** Dispatch the 'loggedin' event so App.svelte can switch views. */
  const { onsuccess } = $props();

  async function login(e) {
    e.preventDefault();
    error   = '';
    loading = true;
    try {
      const resp = await auth(username, password);
      setToken(resp.token);
      token.set(resp.token);

      // Open the WebSocket immediately so that any %prompt messages sent by
      // the Lily server during the SLCP sync (blurb, review, etc.) arrive
      // here and can be auto-answered with a blank line.  Without this,
      // fetchState blocks forever because Lily waits for the prompt reply.
      connect();
      const unsubPrompt = serverPrompt.subscribe(p => {
        if (p) {
          send('');           // blank reply — keeps existing blurb / dismisses review
          serverPrompt.set('');
        }
      });

      // Fetch initial state (blocks until SLCP sync is done on the server).
      const state = await fetchState();
      unsubPrompt();

      whoami.set(state.whoami);
      entities.set(Object.fromEntries(state.entities.map(e => [e.handle, e])));

      // Replay history since last seen position.
      if (state.last_seen_id > 0) {
        lastSeenID.set(state.last_seen_id);
        let afterID = state.last_seen_id;
        while (true) {
          const ev = await fetchEvents(afterID, 200);
          for (const msg of ev.events) processMessage(msg);
          if (!ev.more || ev.events.length === 0) break;
          afterID = ev.events[ev.events.length - 1].id;
        }
      }

      onsuccess();
    } catch (err) {
      disconnect();
      error = err.message || 'Login failed';
    } finally {
      loading = false;
    }
  }
</script>

<div class="login-wrap">
  <div class="login-card">
    <h1>ZephyrLily</h1>
    <p class="subtitle">Sign in to your Lily server</p>

    <form onsubmit={login}>
      <label>
        Username
        <input type="text" bind:value={username} autocomplete="username"
               required placeholder="your lily username" />
      </label>
      <label>
        Password
        <input type="password" bind:value={password}
               autocomplete="current-password" required />
      </label>

      {#if error}
        <p class="error">{error}</p>
      {/if}

      <button type="submit" disabled={loading}>
        {loading ? 'Signing in…' : 'Sign in'}
      </button>
    </form>
  </div>
</div>

<style>
  .login-wrap {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    background: var(--bg-tertiary);
  }
  .login-card {
    background: var(--bg-primary);
    border-radius: 8px;
    padding: 2rem 2.5rem;
    width: 100%;
    max-width: 420px;
    text-align: center;
    box-shadow: 0 8px 24px rgba(0,0,0,.4);
  }
  h1 { color: var(--text-normal); margin: 0 0 .25rem; font-size: 1.8rem; }
  .subtitle { color: var(--text-muted); margin: 0 0 1.5rem; font-size: .9rem; }
  form { display: flex; flex-direction: column; gap: .75rem; }
  label { display: flex; flex-direction: column; gap: .3rem;
          font-size: .8rem; font-weight: 600; color: var(--text-muted);
          text-transform: uppercase; letter-spacing: .05em; text-align: left; }
  input {
    background: var(--bg-input); border: 1px solid var(--bg-tertiary);
    border-radius: 4px; padding: .6rem .75rem;
    color: var(--text-normal); font-size: 1rem;
    outline: none; transition: border-color .15s;
  }
  input:focus { border-color: var(--accent); }
  button {
    margin-top: .5rem; padding: .7rem;
    background: var(--accent); color: #fff;
    border: none; border-radius: 4px;
    font-size: 1rem; font-weight: 600; cursor: pointer;
    transition: filter .15s;
  }
  button:hover:not(:disabled) { filter: brightness(1.1); }
  button:disabled { opacity: .6; cursor: default; }
  .error { color: var(--danger); font-size: .875rem; margin: 0; }
</style>
