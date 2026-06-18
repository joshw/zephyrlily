<script>
  import { send } from '../lib/ws.js';
  import { activeTarget, entities, serverPrompt, whoami, addMessage } from '../lib/store.js';
  import { get } from 'svelte/store';

  const { target } = $props();

  let text = $state('');
  let inputEl;

  function formatTarget(handle) {
    const e = get(entities)[handle];
    if (!e) return handle;
    // Spaces → underscores for Lily's name matching
    const name = e.name.replace(/ /g, '_');
    return e.kind === 'disc' ? `-${name}` : name;
  }

  function submit(e) {
    e.preventDefault();
    const t = text.trim();
    if (!t) return;

    // Commands (/ or %) are forwarded verbatim — not addressed to a destination.
    if (t.startsWith('/') || t.startsWith('%')) {
      send(t);
      text = '';
      return;
    }

    const dest = formatTarget(target);
    send(`${dest};${t}`);

    // Skip local echo when the server will echo the message itself:
    //   - ':' prefix → emote command, echoed as an emote event
    //   - emote discussion (attrib='emote') → all messages echoed as emote events
    const isEmoteDisc = get(entities)[target]?.attrib === 'emote';
    if (!t.startsWith(':') && !isEmoteDisc) {
      const me = get(whoami);
      const ent = get(entities)[target];
      const eventType = ent?.kind === 'disc' ? 'public' : 'private';
      addMessage(target, {
        id: `local-${Date.now()}`,
        event: eventType,
        source: me,
        recips: [target],
        value: t,
        time: Math.floor(Date.now() / 1000),
        stamp: true,
        text: '',
        data: {},
        local: true,
      });
    }

    text = '';
  }

  function onKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit(e);
    }
  }
</script>

<div class="compose">
  {#if $serverPrompt}
    <div class="prompt-banner">{$serverPrompt}</div>
  {/if}
  <form onsubmit={submit}>
    <textarea
      bind:this={inputEl}
      bind:value={text}
      onkeydown={onKeydown}
      placeholder={target ? `Message ${$entities[target]?.name ?? target}` : 'Enter a command or select a conversation'}
      rows="1"
    ></textarea>
    <button type="submit" disabled={!text.trim() || (!target && !text.trim().startsWith('/') && !text.trim().startsWith('%'))} title="Send">
      ➤
    </button>
  </form>
</div>

<style>
  .compose {
    padding: .75rem 1rem;
    background: var(--bg-primary);
    border-top: 1px solid var(--bg-tertiary);
    flex-shrink: 0;
  }
  .prompt-banner {
    background: var(--bg-tertiary); border-radius: 4px;
    padding: .4rem .75rem; margin-bottom: .5rem;
    color: var(--text-muted); font-size: .85rem; font-style: italic;
  }
  form {
    display: flex; align-items: flex-end; gap: .5rem;
    background: var(--bg-input); border-radius: 8px;
    padding: .5rem .75rem;
  }
  textarea {
    flex: 1; background: none; border: none; outline: none;
    color: var(--text-normal); font-size: .95rem;
    resize: none; max-height: 8rem; overflow-y: auto;
    font-family: inherit; line-height: 1.4;
  }
  textarea::placeholder { color: var(--text-muted); }
  textarea:disabled { cursor: not-allowed; }
  button {
    background: none; border: none; cursor: pointer;
    color: var(--text-muted); font-size: 1.2rem; padding: .1rem .2rem;
    transition: color .15s;
    line-height: 1;
  }
  button:hover:not(:disabled) { color: var(--accent); }
  button:disabled { opacity: .3; cursor: default; }
</style>
