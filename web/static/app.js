// Connects to the engine over WebSocket and shows the connection state.
// Plain ES module, no dependencies; see docs/websocket-protocol.md.

const status = document.getElementById("status");
const PING_EVERY_MS = 30000;
const MAX_RETRY_MS = 30000;

let retryMs = 1000;

function show(text, state) {
  status.textContent = text; // never innerHTML: server text is untrusted
  status.dataset.state = state;
}

function connect() {
  const scheme = location.protocol === "https:" ? "wss://" : "ws://";
  const socket = new WebSocket(scheme + location.host + "/ws");
  let pinger;

  socket.addEventListener("open", () => {
    retryMs = 1000;
    pinger = setInterval(() => socket.send(JSON.stringify({ type: "ping" })), PING_EVERY_MS);
  });

  socket.addEventListener("message", (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch {
      return;
    }
    if (msg.type === "welcome") {
      show("connected · engine " + String(msg.version), "online");
    }
  });

  socket.addEventListener("close", () => {
    clearInterval(pinger);
    show("offline · retrying in " + Math.round(retryMs / 1000) + " s", "offline");
    setTimeout(connect, retryMs);
    retryMs = Math.min(retryMs * 2, MAX_RETRY_MS);
  });
}

show("connecting…", "connecting");
connect();
