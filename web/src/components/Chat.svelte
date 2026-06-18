<script>
  import Sidebar from './Sidebar.svelte';
  import MessageList from './MessageList.svelte';
  import Compose from './Compose.svelte';
  import { connect, disconnect, onDisconnect } from '../lib/ws.js';
  import { activeTarget, entities, memberDiscs, clearUnread,
           connected, lastSeenID } from '../lib/store.js';
  import { reportSeen, fetchState, fetchEvents } from '../lib/api.js';
  import { whoami, entities as entitiesStore } from '../lib/store.js';
  import { processMessage } from '../lib/events.js';
  import { get } from 'svelte/store';

  const { onlogout } = $props();

  let showReconnect = $state(false);
  let currentTarget = $state(null);

  // Only open a new WebSocket if the login flow hasn't already opened one.
  // When navigating Login → Chat the socket is already live; calling connect()
  // again would close it and the async onclose would falsely trigger the
  // "Connection lost" banner.
  if (!get(connected)) {
    connect();
    // If we're arriving here without going through Login (e.g. page refresh
    // with a stored token), load state now.
    if (!get(whoami)) {
      fetchState().then(state => {
        whoami.set(state.whoami);
        entitiesStore.set(Object.fromEntries(state.entities.map(e => [e.handle, e])));
        lastSeenID.set(state.last_seen_id);
        if (state.last_seen_id > 0) {
          fetchEvents(state.last_seen_id, 200).then(ev => {
            for (const msg of (ev.events ?? [])) processMessage(msg);
          }).catch(() => {});
        }
      }).catch(() => {});
    }
  }
  onDisconnect(() => { showReconnect = true; });

  // Report seen position periodically, but only when the ID has advanced.
  let lastReportedSeenID = 0;
  const seenInterval = setInterval(() => {
    const id = get(lastSeenID);
    if (id > 0 && id !== lastReportedSeenID) {
      lastReportedSeenID = id;
      reportSeen(id).catch(() => {});
    }
  }, 5000);

  function handleSelect(handle) {
    currentTarget = handle;
    activeTarget.set(handle);
    clearUnread(handle);
  }

  function logout() {
    clearInterval(seenInterval);
    disconnect();
    onlogout();
  }

  function reconnect() {
    showReconnect = false;
    connect();
  }

  function targetTitle(handle) {
    if (!handle || handle === '__system__') return 'Home';
    const e = get(entities)[handle];
    if (!e) return handle;
    if (e.kind === 'disc') return `#${e.name}`;
    return e.name;
  }

  function targetTopic(handle) {
    if (!handle || handle === '__system__') return '';
    return get(entities)[handle]?.title ?? '';
  }

  // Compose should treat '__system__' the same as no conversation selected.
  const composeTarget = $derived(
    currentTarget === '__system__' ? null : currentTarget
  );
</script>

<div class="chat-layout">
  <Sidebar onselect={handleSelect} />

  <div class="main">
    <!-- Top bar -->
    <header class="topbar">
      <div class="channel-info">
        {#if currentTarget}
          <span class="channel-name">{targetTitle(currentTarget)}</span>
          {#if targetTopic(currentTarget)}
            <span class="channel-topic">{targetTopic(currentTarget)}</span>
          {/if}
        {:else}
          <span class="channel-name" style="color:var(--text-muted)">ZephyrLily</span>
        {/if}
      </div>
      <div class="topbar-actions">
        <span class="conn-dot" class:ok={$connected} title={$connected ? 'Connected' : 'Disconnected'}></span>
        <button class="logout-btn" onclick={logout} title="Sign out">⎋</button>
      </div>
    </header>

    {#if showReconnect}
      <div class="reconnect-banner">
        Connection lost.
        <button onclick={reconnect}>Reconnect</button>
        <button onclick={logout}>Sign out</button>
      </div>
    {/if}

    <MessageList target={currentTarget} />
    <Compose target={composeTarget} />
  </div>
</div>

<style>
  .chat-layout {
    display: flex; height: 100vh; width: 100%; overflow: hidden;
  }
  .main {
    flex: 1; display: flex; flex-direction: column; overflow: hidden;
    background: var(--bg-primary);
  }
  .topbar {
    display: flex; align-items: center; justify-content: space-between;
    padding: 0 1rem; height: 48px; flex-shrink: 0;
    border-bottom: 1px solid var(--bg-tertiary);
    background: var(--bg-primary);
    box-shadow: 0 1px 3px rgba(0,0,0,.2);
  }
  .channel-info { display: flex; align-items: baseline; gap: .75rem; min-width: 0; }
  .channel-name {
    font-weight: 700; color: var(--text-normal); font-size: .95rem;
    white-space: nowrap;
  }
  .channel-topic {
    color: var(--text-muted); font-size: .8rem;
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .topbar-actions { display: flex; align-items: center; gap: .75rem; flex-shrink: 0; }
  .conn-dot {
    width: 8px; height: 8px; border-radius: 50%;
    background: var(--text-muted); transition: background .3s;
  }
  .conn-dot.ok { background: var(--online); }
  .logout-btn {
    background: none; border: none; cursor: pointer;
    color: var(--text-muted); font-size: 1.1rem; padding: .2rem .3rem;
    border-radius: 4px; transition: color .15s, background .15s;
  }
  .logout-btn:hover { color: var(--text-normal); background: rgba(255,255,255,.07); }

  .reconnect-banner {
    background: var(--danger); color: #fff;
    padding: .5rem 1rem; text-align: center;
    font-size: .875rem; display: flex; gap: .75rem;
    align-items: center; justify-content: center; flex-shrink: 0;
  }
  .reconnect-banner button {
    background: rgba(255,255,255,.2); border: none; color: #fff;
    padding: .25rem .75rem; border-radius: 4px; cursor: pointer;
    font-weight: 600;
  }
  .reconnect-banner button:hover { background: rgba(255,255,255,.3); }
</style>
