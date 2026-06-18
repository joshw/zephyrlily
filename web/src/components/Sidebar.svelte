<script>
  import { memberDiscs, onlineUsers, activeTarget, unread,
           whoami, entities, entityName } from '../lib/store.js';
  import { get } from 'svelte/store';

  const { onselect } = $props();

  function select(handle) {
    activeTarget.set(handle);
    onselect(handle);
  }

  const HOME = '__system__';

  function stateClass(user) {
    if (user.state === 'here')   return 'here';
    if (user.state === 'away')   return 'away';
    if (user.state === 'detach') return 'detach';
    return 'away';
  }
</script>

<nav class="sidebar">
  <!-- Current user indicator -->
  <div class="me">
    <span class="avatar">{($entities[$whoami]?.name ?? '?')[0].toUpperCase()}</span>
    <div class="me-info">
      <span class="me-name">{$entities[$whoami]?.name ?? $whoami}</span>
      {#if $entities[$whoami]?.blurb}
        <span class="me-blurb">{$entities[$whoami].blurb}</span>
      {/if}
    </div>
  </div>

  <ul class="list home-list">
    <li>
      <button class="item" class:active={!$activeTarget || $activeTarget === HOME}
              onclick={() => select(HOME)}>
        <span class="home-icon">⌂</span>
        <span class="name">Home</span>
        {#if ($unread[HOME] ?? 0) > 0}
          <span class="badge">{$unread[HOME]}</span>
        {/if}
      </button>
    </li>
  </ul>

  <div class="section-label">Discussions</div>
  <ul class="list">
    {#each $memberDiscs as disc (disc.handle)}
      <li>
        <button class="item" class:active={$activeTarget === disc.handle}
                onclick={() => select(disc.handle)}>
          <span class="hash">#</span>
          <span class="name">{disc.name}</span>
          {#if ($unread[disc.handle] ?? 0) > 0}
            <span class="badge">{$unread[disc.handle]}</span>
          {/if}
        </button>
      </li>
    {/each}
  </ul>

  <div class="section-label">Users</div>
  <ul class="list">
    {#each $onlineUsers.filter(u => u.handle !== $whoami) as user (user.handle)}
      <li>
        <button class="item user-item" class:active={$activeTarget === user.handle}
                onclick={() => select(user.handle)}>
          <span class="dot {stateClass(user)}"></span>
          <span class="name">{user.name}</span>
          {#if user.blurb}
            <span class="blurb">{user.blurb}</span>
          {/if}
          {#if ($unread[user.handle] ?? 0) > 0}
            <span class="badge">{$unread[user.handle]}</span>
          {/if}
        </button>
      </li>
    {/each}
  </ul>
</nav>

<style>
  .sidebar {
    width: 230px; flex-shrink: 0;
    background: var(--bg-secondary);
    display: flex; flex-direction: column;
    overflow-y: auto; padding-bottom: 1rem;
  }
  .me {
    display: flex; align-items: center; gap: .6rem;
    padding: .75rem 1rem; background: var(--bg-tertiary);
    border-bottom: 1px solid rgba(0,0,0,.2);
    flex-shrink: 0;
  }
  .avatar {
    width: 32px; height: 32px; border-radius: 50%;
    background: var(--accent); color: #fff;
    display: flex; align-items: center; justify-content: center;
    font-weight: 700; font-size: .9rem; flex-shrink: 0;
  }
  .me-info { display: flex; flex-direction: column; min-width: 0; }
  .me-name { font-weight: 600; font-size: .85rem; color: var(--text-normal);
             white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .me-blurb { font-size: .7rem; color: var(--text-muted);
              white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  .home-list { padding-top: .4rem; }
  .home-icon { font-size: 1rem; flex-shrink: 0; line-height: 1; }

  .section-label {
    padding: 1rem 1rem .25rem;
    font-size: .7rem; font-weight: 700;
    color: var(--text-muted); text-transform: uppercase; letter-spacing: .05em;
  }
  .list { list-style: none; margin: 0; padding: 0; }
  li { list-style: none; }
  .item {
    display: flex; align-items: center; gap: .4rem;
    padding: .3rem .75rem; border-radius: 4px;
    margin: 1px .5rem; cursor: pointer; width: calc(100% - 1rem);
    color: var(--text-muted); font-size: .9rem;
    background: none; border: none; text-align: left;
    transition: background .1s, color .1s;
  }
  .item:hover { background: rgba(255,255,255,.07); color: var(--interactive-hover); }
  .item.active { background: rgba(255,255,255,.1); color: var(--text-normal); }
  .hash { color: var(--text-muted); font-size: .9rem; flex-shrink: 0; }
  .name { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; flex: 1; }
  .blurb { font-size: .75rem; color: var(--text-muted);
           white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .badge {
    background: var(--danger); color: #fff;
    border-radius: 9999px; font-size: .65rem; font-weight: 700;
    padding: .1rem .35rem; flex-shrink: 0;
  }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
  .dot.here   { background: var(--online); }
  .dot.away   { background: var(--idle); }
  .dot.detach { background: var(--text-muted); }
</style>
