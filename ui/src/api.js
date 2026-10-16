// Shared JSON fetch helpers for the gateway API.

const jsonInit = (body) => ({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

// postJSON: POST body as JSON and return the parsed reply whatever the HTTP
// status (the gateway answers errors as {success: false, error}). Throws on a
// network error or a non-JSON reply -- callers keep their own try/catch.
export async function postJSON(url, body) {
  return (await fetch(url, jsonInit(body))).json();
}

// safeFetchJson: fetch and parse JSON, throwing on a non-JSON reply or a
// non-2xx status (with the backend's error text when it has one).
export async function safeFetchJson(url, options = {}) {
  const res = await fetch(url, options);
  const text = await res.text();

  let data;
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    throw new Error(`Invalid backend response from ${url}`);
  }

  if (!res.ok) {
    throw new Error(data?.error || data?.message || `HTTP ${res.status}`);
  }

  return data;
}

// postJSONStrict: safeFetchJson as a JSON POST.
export const postJSONStrict = (url, body) => safeFetchJson(url, jsonInit(body));
