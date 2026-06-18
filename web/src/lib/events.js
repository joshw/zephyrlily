/**
 * Process incoming WSServerMsg events and update the Svelte store.
 * Public/private/emote/pa are shown in the message stream.
 * All other event types display as the proxy's pre-formatted `text` field.
 */
import {
  whoami, entities, addMessage, bumpUnread, activeTarget, lastSeenID,
} from './store.js';
import { get } from 'svelte/store';

let _lastSeenID = 0;
lastSeenID.subscribe(v => { _lastSeenID = v; });

// Called for every WSServerMsg (both history replay and live WebSocket).
export function processMessage(wsMsg) {
  const id = wsMsg.id ?? 0;

  switch (wsMsg.type) {
    case 'event':
      handleEvent(wsMsg.data, id);
      break;
    case 'text': {
      // Raw server text — route to active conversation so it's visible.
      const txt = wsMsg.data?.text ?? '';
      if (txt) addMessage(get(activeTarget) ?? '__system__', { id, kind: 'text', text: txt });
      break;
    }
    case 'commandresult': {
      const lines = wsMsg.data?.lines ?? [];
      // Suppress Lily's send-confirmation lines — redundant since the web UI
      // already shows sent messages optimistically.
      if (lines.every(l => l.startsWith('(message sent to'))) break;
      addMessage(get(activeTarget) ?? '__system__', { id, kind: 'commandresult', lines });
      break;
    }
    case 'prompt':
      // handled in ws.js → serverPrompt store
      break;
    case 'error':
      addMessage(get(activeTarget) ?? '__system__', { id, kind: 'error', text: wsMsg.data ?? '' });
      break;
  }

  if (id > _lastSeenID) {
    lastSeenID.set(id);
  }
}

function handleEvent(data, id) {
  if (!data?.notify) return;   // server said don't display

  const event   = data.event  ?? '';
  const source  = data.source ?? '';
  const recips  = data.recips ?? [];
  const value   = data.value  ?? '';
  const me      = get(whoami);

  // Update entity state first
  applyEntityUpdates(data);

  // Determine the conversation target (disc or private peer).
  const target = resolveTarget(event, source, recips, me);

  const msg = { id, event, source, recips, value, time: data.time,
                stamp: data.stamp, text: data.text ?? '', data };

  addMessage(target, msg);
  bumpUnread(target);
}

/** Pick which conversation bucket this event belongs to. */
function resolveTarget(event, source, recips, me) {
  if (event === 'public' || event === 'emote') {
    // Destination is the first disc in recips, or fall back to source
    const disc = (recips ?? []).find(h => h.startsWith('#'));
    return disc ?? source;
  }
  if (event === 'private') {
    // Private: if I'm the source, target is the recipient; otherwise the sender
    return source === me
      ? (recips[0] ?? source)
      : source;
  }
  // All other events: pre-rendered text goes to the system/home bucket.
  return '__system__';
}

/** Apply any entity mutations carried in the event. */
function applyEntityUpdates(data) {
  const event  = data.event ?? '';
  const source = data.source ?? '';
  const value  = data.value  ?? '';

  // Merge the embedded entity snapshot into our store
  if (data.entities) {
    entities.update(prev => ({ ...prev, ...data.entities }));
  }

  // Additional mutations not covered by embedded entities
  entities.update(prev => {
    const updated = { ...prev };
    const e = updated[source] ? { ...updated[source] } : null;
    if (!e) return prev;

    switch (event) {
      case 'rename': e.name  = value;  break;
      case 'blurb':  e.blurb = value;  break;
      case 'here':   e.state = 'here'; break;
      case 'away':   e.state = 'away'; break;
      case 'disconnect': e.state = 'away'; break;
    }
    updated[source] = e;
    return updated;
  });
}
