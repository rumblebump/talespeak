// The WebSocket connection to the engine: reconnects with backoff and keeps
// itself alive with pings. See docs/websocket-protocol.md.

const PING_EVERY_MS = 30000;
const MAX_RETRY_MS = 30000;

// The engine closes with this code when the player connected again
// elsewhere; reconnecting would take the game back from there.
const REPLACED = 4001;

/**
 * Connects to /ws on this page's origin and keeps reconnecting until
 * stopped. Before each retry, canRetry is asked: false stops (the player
 * was signed out, or may no longer play).
 * @param {{ onOpen: () => void, onMessage: (msg: object) => void, onClose: (retryMs: number) => void,
 *           onReplaced: () => void, canRetry: () => Promise<boolean> }} handlers
 * @returns {{ send: (msg: object) => boolean, stop: () => void }}
 */
export function connect(handlers) {
  let socket = null;
  let retryMs = 1000;
  let stopped = false;

  function open() {
    if (stopped) return;
    const scheme = location.protocol === "https:" ? "wss://" : "ws://";
    const ws = new WebSocket(scheme + location.host + "/ws");
    socket = ws;
    let pinger;

    ws.addEventListener("open", () => {
      retryMs = 1000;
      pinger = setInterval(() => send({ type: "ping" }), PING_EVERY_MS);
      handlers.onOpen();
    });

    ws.addEventListener("message", (event) => {
      if (typeof event.data !== "string") return;
      let msg;
      try {
        msg = JSON.parse(event.data);
      } catch {
        return;
      }
      if (msg && typeof msg === "object" && !Array.isArray(msg) && typeof msg.type === "string") {
        handlers.onMessage(msg);
      }
    });

    ws.addEventListener("close", (event) => {
      clearInterval(pinger);
      if (socket === ws) socket = null;
      if (stopped) return;
      if (event.code === REPLACED) {
        stopped = true;
        handlers.onReplaced();
        return;
      }
      const wait = retryMs;
      handlers.onClose(wait);
      retryMs = Math.min(retryMs * 2, MAX_RETRY_MS);
      setTimeout(async () => {
        let ok = true;
        try {
          ok = await handlers.canRetry();
        } catch {
          ok = true; // the server is not reachable yet: keep trying
        }
        if (ok) open();
        else stopped = true;
      }, wait);
    });
  }

  function send(msg) {
    if (!socket || socket.readyState !== WebSocket.OPEN) return false;
    socket.send(JSON.stringify(msg));
    return true;
  }

  function stop() {
    stopped = true;
    if (socket) socket.close(1000, "signed out");
  }

  open();
  return { send, stop };
}
