import { writable, derived, get } from 'svelte/store';

// ── Auth ──────────────────────────────────────────────────────────────────────
export const token     = writable(sessionStorage.getItem('zlily_token') ?? '');
export const whoami    = writable('');   // current user handle
export const connected = writable(false);
export const serverPrompt = writable('');

token.subscribe(t => {
  if (t) sessionStorage.setItem('zlily_token', t);
  else   sessionStorage.removeItem('zlily_token');
});

// ── Entities ──────────────────────────────────────────────────────────────────
// Map of handle → EntityJSON
export const entities = writable({});

export const memberDiscs = derived(entities, $e =>
  Object.values($e)
    .filter(e => e.kind === 'disc' && e.member)
    .sort((a, b) => a.name.localeCompare(b.name))
);

export const onlineUsers = derived(entities, $e =>
  Object.values($e)
    .filter(e => e.kind === 'user')
    .sort((a, b) => a.name.localeCompare(b.name))
);

// Convenience: look up display name for a handle
export function entityName(handle) {
  const e = get(entities)[handle];
  return e?.name ?? handle;
}

// ── Messages ──────────────────────────────────────────────────────────────────
// Map of target → array of message objects
// target is a disc handle ("#3324") or user handle ("#850")
export const messages   = writable({});
export const activeTarget = writable(null);   // disc/user handle currently open
export const unread     = writable({});        // handle → unread count

export function addMessage(target, msg) {
  messages.update(m => {
    const list = m[target] ?? [];
    return { ...m, [target]: [...list, msg] };
  });
}

export function clearUnread(handle) {
  unread.update(u => ({ ...u, [handle]: 0 }));
}

export function bumpUnread(handle) {
  const active = get(activeTarget);
  if (active === handle) return;   // already viewing
  unread.update(u => ({ ...u, [handle]: (u[handle] ?? 0) + 1 }));
}

// ── Last seen ID (for /seen reporting) ────────────────────────────────────────
export const lastSeenID = writable(0);
