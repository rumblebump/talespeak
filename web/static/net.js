// The WebSocket connection to the engine: reconnects with backoff and keeps
// itself alive with pings. See docs/websocket-protocol.md.

const PING_EVERY_MS = 30000;
const MAX_RETRY_MS = 30000;

/**
 * Connects to /ws on this page's origin and keeps reconnecting.
 * @param {{ onOpen: () => void, onMessage: (msg: object) => void, onClose: (retryMs: number) => void }} handlers
 * @returns {{ send: (msg: object) => boolean }}
 */
export function connect(handlers) {
  let socket = null;
  let retryMs = 1000;

  function open() {
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

    ws.addEventListener("close", () => {
      clearInterval(pinger);
      if (socket === ws) socket = null;
      handlers.onClose(retryMs);
      setTimeout(open, retryMs);
      retryMs = Math.min(retryMs * 2, MAX_RETRY_MS);
    });
  }

  function send(msg) {
    if (!socket || socket.readyState !== WebSocket.OPEN) return false;
    socket.send(JSON.stringify(msg));
    return true;
  }

  open();
  return { send };
}
