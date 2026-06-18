import { connected, serverPrompt } from './store.js';
import { processMessage } from './events.js';
import { getToken } from './api.js';

let ws = null;
let reconnectTimer = null;
let onDisconnectCallback = null;

export function onDisconnect(fn) { onDisconnectCallback = fn; }

export function connect() {
  if (ws) ws.close();
  clearTimeout(reconnectTimer);

  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const url   = `${proto}//${location.host}/ws?token=${getToken()}`;

  ws = new WebSocket(url);

  ws.onopen = () => {
    connected.set(true);
  };

  ws.onclose = () => {
    connected.set(false);
    ws = null;
    if (onDisconnectCallback) onDisconnectCallback();
  };

  ws.onerror = err => console.error('WebSocket error', err);

  ws.onmessage = ev => {
    try {
      const msg = JSON.parse(ev.data);
      if (msg.type === 'prompt') {
        serverPrompt.set(typeof msg.data === 'string' ? msg.data : '');
      } else {
        processMessage(msg);
      }
    } catch (e) {
      console.error('ws message parse error', e);
    }
  };
}

export function send(text) {
  if (ws?.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type: 'command', text }));
  }
}

export function disconnect() {
  clearTimeout(reconnectTimer);
  if (ws) { ws.close(); ws = null; }
  connected.set(false);
}
