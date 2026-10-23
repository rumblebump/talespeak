// The one way the client reaches the account API (docs/accounts.md).
// scripts/check-web.sh allows fetch only here, on the path checked below.

/**
 * api calls the account API: GET without a body, else a JSON POST. It
 * returns the answer, or throws an Error whose message the player can
 * read.
 * @param {string} path
 * @param {object} [body]
 */
export async function api(path, body) {
  // Only this server's account API: a fixed path, never one from the server.
  const apiPath = /^\/api\/[a-z]+(\?page=[1-9][0-9]{0,3})?$/.test(path) ? path : "/api/invalid";
  const init = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  let resp;
  try {
    resp = await fetch(apiPath, { ...init, credentials: "same-origin", cache: "no-store" });
  } catch {
    throw new Error("The server cannot be reached. Try again in a moment.");
  }
  let data = null;
  try {
    data = await resp.json();
  } catch {
    // not JSON: a proxy's error page
  }
  if (!resp.ok || !data || typeof data !== "object") {
    throw new Error(data && typeof data.error === "string" ? data.error : "Something went wrong (" + resp.status + ").");
  }
  return data;
}
