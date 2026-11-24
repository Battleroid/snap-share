// api calls a JSON endpoint with the session cookie. It never throws: the
// result is { ok, status, data, error }, where error is the server's
// message (or a generic one) for anything but a 2xx.
export async function api(method, url, body) {
  let res;
  try {
    res = await fetch(url, {
      method,
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    return { ok: false, error: "The server could not be reached." };
  }
  let data = null;
  try { data = await res.json(); } catch { /* 204 */ }
  return { ok: res.ok, status: res.status, data, error: data?.error || `Request failed (${res.status}).` };
}
