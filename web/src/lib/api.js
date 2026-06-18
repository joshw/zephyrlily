/** REST API client for the zlily proxy. */

let _token = '';

export function setToken(t) { _token = t; }
export function getToken()  { return _token; }

async function request(method, path, body) {
  const opts = {
    method,
    headers: { Authorization: `Bearer ${_token}` },
  };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(path, opts);
  if (!resp.ok) {
    const msg = await resp.text();
    throw new Error(msg || resp.statusText);
  }
  if (resp.status === 204) return null;
  return resp.json();
}

export const auth = (username, password) =>
  request('POST', '/auth', { username, password });

export const fetchState = () =>
  request('GET', '/state');

export const fetchEvents = (after = 0, limit = 200) =>
  request('GET', `/events?after=${after}&limit=${limit}`);

export const reportSeen = (lastSeenID) =>
  request('POST', '/seen', { last_seen_id: lastSeenID });

export const expand = (q, validDestOnly = true) =>
  request('GET', `/expand?q=${encodeURIComponent(q)}&valid_dest_only=${validDestOnly ? 1 : 0}`);

export const fetchContent = (type, target = 'me', name = '') => {
  let url = `/fetch?type=${encodeURIComponent(type)}&target=${encodeURIComponent(target)}`;
  if (name) url += `&name=${encodeURIComponent(name)}`;
  return request('GET', url);
};

export const storeContent = (type, target, name, lines) =>
  request('POST', '/store', { type, target, name, lines });
