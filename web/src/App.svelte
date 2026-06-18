<script>
  import Login from './components/Login.svelte';
  import Chat  from './components/Chat.svelte';
  import { token, whoami, entities } from './lib/store.js';
  import { setToken } from './lib/api.js';
  import { get } from 'svelte/store';

  // Restore token from sessionStorage on load.
  setToken(get(token));

  let view = get(token) ? 'chat' : 'login';

  function handleLogin() {
    view = 'chat';
  }
  function handleLogout() {
    token.set('');
    whoami.set('');
    entities.set({});
    view = 'login';
  }
</script>

{#if view === 'login'}
  <Login onsuccess={handleLogin} />
{:else}
  <Chat onlogout={handleLogout} />
{/if}

<style>
  :global(*) { box-sizing: border-box; margin: 0; padding: 0; }
  :global(body) {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg-primary);
    color: var(--text-normal);

    /* Discord-like dark theme */
    --bg-primary:   #36393f;
    --bg-secondary: #2f3136;
    --bg-tertiary:  #202225;
    --bg-input:     #40444b;
    --text-normal:  #dcddde;
    --text-muted:   #72767d;
    --interactive-hover: #dcddde;
    --accent:  #7289da;
    --online:  #43b581;
    --idle:    #faa61a;
    --danger:  #f04747;
  }
</style>
