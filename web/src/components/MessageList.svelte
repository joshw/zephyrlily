<script>
  import { messages, entities, whoami } from '../lib/store.js';
  import { get } from 'svelte/store';

  const { target } = $props();

  let listEl = $state(null);

  // Auto-scroll to bottom when messages change.
  $effect(() => {
    const _ = targetMessages.length;
    if (listEl) listEl.scrollTop = listEl.scrollHeight;
  });

  // '__system__' is the Home bucket; null falls back to it for the initial view.
  const targetMessages = $derived(
    (target && target !== '__system__')
      ? ($messages[target] ?? [])
      : ($messages['__system__'] ?? [])
  );

  function entityName(handle) {
    return $entities[handle]?.name ?? handle;
  }

  function formatTime(ts) {
    if (!ts) return '';
    const d = new Date(ts * 1000);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  // Should we group this message with the previous one (same sender, close in time)?
  function shouldGroup(msg, prev) {
    if (!prev || !prev.source || !msg.source) return false;
    if (msg.source !== prev.source) return false;
    if (msg.event !== prev.event) return false;
    return (msg.time - prev.time) < 300; // within 5 minutes
  }
</script>

<div class="message-list" bind:this={listEl}>
  {#if targetMessages.length === 0}
    <div class="empty">
      {(target && target !== '__system__') ? 'No messages yet.' : 'No activity yet.'}
    </div>
  {:else}
    {#each targetMessages as msg, i (msg.id ?? i)}
      {#if msg.kind === 'text'}
        <!-- Plain server text (login banners, etc.) -->
        <div class="server-text">{msg.text}</div>

      {:else if msg.kind === 'error'}
        <div class="error-line">⚠ {msg.text}</div>

      {:else if msg.kind === 'commandresult'}
        <div class="command-result">
          <div class="command-result-label">— command output (only you see this) —</div>
          {#each msg.lines as line}<div>{line}</div>{/each}
        </div>

      {:else if msg.event === 'public' || msg.event === 'private'}
        {@const grouped = shouldGroup(msg, targetMessages[i-1])}
        <div class="message" class:grouped>
          {#if !grouped}
            <div class="message-header">
              <span class="sender" class:me={msg.source === $whoami}>
                {entityName(msg.source)}
                {#if $entities[msg.source]?.blurb}
                  <span class="blurb">[{$entities[msg.source].blurb}]</span>
                {/if}
              </span>
              {#if msg.stamp && msg.time}
                <span class="ts">{formatTime(msg.time)}</span>
              {/if}
            </div>
          {/if}
          <div class="body">{msg.value}</div>
        </div>

      {:else if msg.event === 'emote'}
        <div class="emote">
          <span class="emote-sender">{entityName(msg.source)}</span>
          {msg.value}
          {#if msg.stamp && msg.time}
            <span class="ts">{formatTime(msg.time)}</span>
          {/if}
        </div>

      {:else if msg.event === 'pa'}
        <div class="pa">
          <span class="pa-label">Public address from {entityName(msg.source)}:</span>
          {msg.value}
        </div>

      {:else}
        <!-- All other events: use proxy-formatted text -->
        <div class="event-line">{msg.text}</div>
      {/if}
    {/each}
  {/if}
</div>

<style>
  .message-list {
    flex: 1; overflow-y: auto; padding: 1rem;
    display: flex; flex-direction: column; gap: .1rem;
  }
  .empty {
    color: var(--text-muted); text-align: center;
    margin: auto; font-size: .9rem;
  }

  /* Chat messages */
  .message { padding: .25rem 0; }
  .message.grouped { padding-top: .1rem; }
  .message-header {
    display: flex; align-items: baseline; gap: .5rem; margin-bottom: .1rem;
  }
  .sender { font-weight: 600; color: var(--text-normal); font-size: .9rem; }
  .sender.me { color: var(--accent); }
  .blurb { font-weight: 400; color: var(--text-muted); font-size: .8rem; }
  .ts { font-size: .7rem; color: var(--text-muted); }
  .body { color: var(--text-normal); font-size: .9rem; line-height: 1.4;
          white-space: pre-wrap; word-break: break-word; }

  /* Emotes */
  .emote {
    color: var(--text-muted); font-style: italic;
    padding: .2rem 0; font-size: .9rem;
  }
  .emote-sender { color: var(--text-normal); font-style: normal; font-weight: 600; margin-right: .3rem; }

  /* Public address */
  .pa {
    background: rgba(114,137,218,.1); border-left: 3px solid var(--accent);
    padding: .4rem .75rem; border-radius: 0 4px 4px 0;
    color: var(--text-normal); font-size: .9rem;
  }
  .pa-label { font-weight: 600; color: var(--accent); display: block;
              font-size: .8rem; margin-bottom: .1rem; }

  /* Event lines (connect, rename, etc.) */
  .event-line {
    color: var(--text-muted); font-size: .8rem; font-style: italic;
    padding: .1rem 0; text-align: center;
  }

  /* Server text / command results */
  .server-text {
    font-family: monospace; font-size: .85rem;
    color: var(--text-muted); padding: .1rem 0; white-space: pre-wrap;
  }
  .command-result {
    font-family: monospace; font-size: .85rem;
    color: var(--text-muted); padding: .4rem .75rem;
    background: rgba(0,0,0,.25);
    border: 1px solid rgba(255,255,255,.06);
    border-left: 3px solid var(--text-muted);
    border-radius: 0 4px 4px 0;
    white-space: pre-wrap; margin: .2rem 0;
  }
  .command-result-label {
    font-size: .7rem; color: var(--text-muted); opacity: .6;
    margin-bottom: .3rem; font-style: italic; font-family: inherit;
  }
  .error-line {
    color: var(--danger); font-size: .85rem; padding: .2rem 0;
  }
</style>
