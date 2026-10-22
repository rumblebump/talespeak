# Web client

The browser client in `web/static/` is a renderer: it draws the messages the engine sends ([websocket-protocol.md](websocket-protocol.md)) and sends back what the player clicks and types. It is embedded in the binary and loads nothing from another origin.

## Files

| File | What it does |
| --- | --- |
| `index.html` | The page; Alpine attributes hold only simple expressions |
| `app.js` | The `game` Alpine component: signing in, the lobby, every renderer and all input handling |
| `net.js` | The WebSocket to one world: reconnects with backoff (1 s doubling to 30 s), pings every 30 s, stops after 4001 (playing elsewhere) or when the player may no longer play |
| `speech.js` | Read aloud with the browser's local voices |
| `style.css` | Terminal theme on CSS variables: dark, light (follows the system), large text |
| `vendor/alpine-csp.js` | Alpine.js CSP build, pinned in `web/vendor.json` |

Plain ES modules, no build step. The CSP build of Alpine runs without `'unsafe-eval'`; it refuses `x-html` and access to globals in attributes, so all logic lives in `app.js`. Untrusted text (pack text, typed input, later chat and LLM output) only reaches the page through `x-text` or `textContent`.

## Signing in

The page asks `/api/me` first ([accounts.md](accounts.md)) and shows one of:

- **Sign in**: name and password, with **new account** when the server allows registering. Errors from the server are shown under the form.
- **The lobby** (`GET /api/worlds`): a card per world the account may see, its own worlds first, each with its name, pack, description, public or private, its game master, players and players online now, since when it runs, and the account's role; **play**, and for its game masters **join code** (a code for 30 players, shown on the card with **copy**) and **end world** (after a confirmation). Below: **Join with a code**, and **New world** (pack, optional name, public or private) when the account may create worlds. The code of a new world is shown on its card.
- **The game**, with the account name, **worlds** (back to the lobby), **sign out**, and **invite** for admins and game masters (a code for one player, shown in the story). The page's address names the world (`#world=12`), so a reload continues there; the lobby and signing out clear it.
- **Playing elsewhere**: after another tab or device took over, with **play here**.

The only `fetch` the client makes is to these API paths (`scripts/check-web.sh` checks it).

## What the player can do

| Action | Mouse or touch | Keyboard |
| --- | --- | --- |
| Use an item | Click it | Tab to it, Enter |
| Answer a dialog | Click the answer | Its number key, or type the number or the answer's text and Enter |
| Multiple choice | Tick the boxes, then "ok" | Number keys tick and untick, Enter confirms; or type "1, 3" and Enter |
| Answer a typed question | | Type it, Enter (number keys type digits here) |
| Say what you do | | Type a line, Enter; arrow up and down go through earlier lines |
| Close a picture | "close" | Escape |

While a question is open, items cannot be used. A strict question (`ui.ask` with `strict`) also hides the items, so their labels cannot give the answer away, and turns off autocorrect.

## Settings

Saved in the browser (`localStorage`), and still working for the open page when the browser refuses to save them:

- **large text** makes everything about a third larger.
- **read aloud** reads new story lines, questions and their options aloud. Only voices that run on the device are used (`localService`); some browsers also list cloud voices, which would send the story to a third party. Without a local voice the button is hidden, and lines a pack wants only spoken are shown as text.

Speech to text comes with T-0017.

## Vendored code

`scripts/vendor.sh check` compares each vendored file with its SHA-256 in `web/vendor.json` (offline, in `scripts/check-web.sh`); `scripts/vendor.sh verify` downloads the pinned version from npm and compares the bytes (CI `web` job). To update: `scripts/vendor.sh update alpine-csp 3.18.0`, then review the diff. Renovate proposes new versions by bumping `web/vendor.json`; `verify` fails on its PR until someone runs `update` on the branch.

`scripts/check-web.sh` also fails on any external URL in `web/` (the AGPL source link in the footer is the one exception: it is followed, never loaded) and on HTML sinks such as `innerHTML` or `x-html`.
