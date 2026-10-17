// Connects to the engine over WebSocket, shows the story text and answers
// dialogs. Plain ES module, no dependencies; see docs/websocket-protocol.md.
// Server text is untrusted: it only ever goes into textContent.

const status = document.getElementById("status");
const story = document.getElementById("story");
const dialog = document.getElementById("dialog");
const dialogTitle = document.getElementById("dialog-title");
const dialogOptions = document.getElementById("dialog-options");
const PING_EVERY_MS = 30000;
const MAX_RETRY_MS = 30000;

let retryMs = 1000;

function show(text, state) {
  status.textContent = text; // never innerHTML: server text is untrusted
  status.dataset.state = state;
}

function line(text, className) {
  const p = document.createElement("p");
  p.textContent = String(text);
  if (className) p.className = className;
  story.append(p);
}

function hideDialog() {
  dialog.hidden = true;
  dialogOptions.replaceChildren();
}

// showDialog lists the options as buttons; a click or the option's number
// key sends the choice once.
function showDialog(socket, msg) {
  dialogTitle.textContent = String(msg.title);
  dialogOptions.replaceChildren();
  const options = Array.isArray(msg.options) ? msg.options : [];
  const choose = (n) => {
    hideDialog();
    document.removeEventListener("keydown", onKey);
    socket.send(JSON.stringify({ type: "choose", id: String(msg.id), option: n }));
  };
  const onKey = (event) => {
    const n = Number(event.key);
    if (Number.isInteger(n) && n >= 1 && n <= options.length) choose(n);
  };
  options.forEach((label, i) => {
    const li = document.createElement("li");
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = String(label);
    button.addEventListener("click", () => choose(i + 1));
    li.append(button);
    dialogOptions.append(li);
  });
  document.addEventListener("keydown", onKey);
  socket.addEventListener("close", () => document.removeEventListener("keydown", onKey), { once: true });
  dialog.hidden = false;
  dialogOptions.querySelector("button")?.focus();
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
    switch (msg.type) {
      case "welcome":
        show("connected · engine " + String(msg.version), "online");
        story.replaceChildren(); // a new connection starts a new session
        hideDialog();
        break;
      case "room":
        line(msg.title, "room-title");
        line(msg.text);
        break;
      case "text":
        line(msg.text);
        break;
      case "dialog":
        showDialog(socket, msg);
        break;
      case "error":
        line(msg.message, "error");
        break;
    }
  });

  socket.addEventListener("close", () => {
    clearInterval(pinger);
    hideDialog();
    show("offline · retrying in " + Math.round(retryMs / 1000) + " s", "offline");
    setTimeout(connect, retryMs);
    retryMs = Math.min(retryMs * 2, MAX_RETRY_MS);
  });
}

show("connecting…", "connecting");
connect();
