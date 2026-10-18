# WebSocket protocol

The browser talks to the engine over one WebSocket at `/ws` on the same origin. Upgrades from another origin are refused.

Every message, in both directions, is one JSON object with a `type` field.

## Limits

Admins can change these defaults with environment variables ([admin-limits.md](admin-limits.md)); the message size stays fixed.

| Limit | Default | When exceeded |
| --- | --- | --- |
| Open connections per server | 1000 | Upgrade refused with HTTP 503 |
| Open connections per client address | 100 | Upgrade refused with HTTP 503 |
| New connections per client address | 60 at once, then 1 per second | Upgrade refused with HTTP 429 and `Retry-After` |
| Message size from the client | 16 KiB | Connection closed (1009, message too big) |
| Messages per connection | 20 at once, then 10 per second | Connection closed (1008, too many messages) |
| Script time per client address | 2 s at once, then 250 ms per second | The message gets an `error` ("Too much at once: wait a moment, then try again."); a question stays open |
| Silence from the client | 2 minutes | Connection closed (1008, idle timeout); clients ping every 30 s |
| Binary frame, or text that is not one JSON object (including `null` and arrays) | | Connection closed (1003) |

Per-client limits count IPv6 clients by their /64 network; the Lua time budget counts them by /48, since one home or school often has a /56 or a /48. The client address is the TCP peer, unless the peer is a trusted proxy: then it is the rightmost `X-Forwarded-For` entry that is not a trusted proxy. `TALESPEAK_TRUSTED_PROXIES` (comma-separated CIDR prefixes) sets the trusted proxies; the default is loopback and the private ranges, which covers Caddy in the same Docker network. Narrow it to the proxy's own address when untrusted clients share the private network with the engine; an unset or empty value means the default. The per-client limit stays well above a school class, because a class behind one NAT address shares it.

## Messages

| Direction | `type` | Fields | Meaning |
| --- | --- | --- | --- |
| server → client | `welcome` | `version` | Sent once after connecting; a new session starts in the pack's start room |
| client → server | `ping` | | Keep-alive |
| server → client | `pong` | | Answer to `ping` |
| server → client | `room` | `id`, `title`, `text`, `items` | The room the player is in, with the things they can click |
| server → client | `text` | `text`, `speak`, `hide` | A line of story text. `speak`: read it aloud even when read aloud is off. `hide`: only read it aloud (a listening exercise); show it when the device has no local voice |
| server → client | `items` | `items` | Replaces the things the player can click; none clears them |
| server → client | `image` | `url`, `text` | Shows one of the pack's images as a popup; `text` is its caption and alt text |
| server → client | `dialog` | `id`, `title`, `options` (1 to 9 strings), `speak`, `hide` | A question with one answer; the script waits for it |
| server → client | `select` | `id`, `title`, `options` (1 to 9 strings), `min`, `max`, `speak`, `hide` | A question where the player picks `min` (0 when absent) to `max` options |
| server → client | `ask` | `id`, `title`, `strict`, `speak`, `hide` | A question the player answers by typing. `strict`: no typing help (no autocorrect, autocomplete or suggestions, clickable items hidden), for spelling practice and riddles |
| client → server | `choose` | `id`, `option` (1-based) for `dialog`; `id`, `choices` (list of option numbers) for `select` | The answer to the open dialog or select |
| client → server | `answer` | `id`, `text` | The typed answer to the open `ask` |
| client → server | `input` | `text` | A line the player typed, outside a question; runs the room's `on_input` |
| client → server | `click` | `item` | The player clicked one of the items they were shown; runs the room's `on_click` |
| server → client | `error` | `message`, `id` | The client sent something the server does not understand or cannot take now, or a room script failed; the connection stays open. `id` is set only when an answer to the open question was not taken: the question with that `id` is still open and can be answered again |

An item is `{"id": "straw", "label": "heap of straw"}`. `speak` and `hide` on a question work as on `text`, for its title.

A question's `id` is valid until it is answered: an answer with an older `id`, the wrong kind of answer, an option out of range, or a number of choices outside `min` to `max` gets an `error` and the question stays open; an option or answer the open question refuses (or one sent while the address used up its script time) gets an `error` with that question's `id`. An `error` without `id` after an answer means the question is gone, for example because the script failed. While a question is open, `input` and `click` get an `error` ("Answer the open question first."). `input` and `answer` take at most 500 bytes; control characters become spaces and the text is trimmed. When the room has no `on_input` or `on_click`, the answer is the text "Nothing happens.".

Text fields are plain text: clients show them with `textContent` or `x-text`, never as HTML. An image `url` always has the form `/packs/<pack id>/assets/<file>` on the same server; the client refuses any other.

Playing the demo:

```text
< {"type":"welcome","version":"local"}
< {"type":"room","id":"cell","title":"A damp cell","text":"Stone walls, ...","items":[{"id":"straw","label":"heap of straw"},{"id":"guard","label":"sleepy guard"},{"id":"scratches","label":"scratches on the wall"},{"id":"lock","label":"lock with letter wheels"}]}
< {"type":"text","text":"A guard leans against the bars, half asleep."}
< {"type":"text","text":"Click something in the cell, or type what you do."}
> {"type":"click","item":"guard"}
< {"type":"dialog","id":"d1","title":"What do you say to the guard, traveler?","options":["Ask for water","Ask what time it is","Say nothing"]}
> {"type":"choose","id":"d1","option":1}
< {"type":"text","text":"The guard grunts and slides a cup of water through the bars."}
> {"type":"input","text":"search the straw"}
< {"type":"select","id":"d2","title":"Under the straw you find a few things. What do you keep? Pick up to two.","options":["A bent nail","A smooth pebble","A torn piece of cloth"],"min":1,"max":2}
> {"type":"choose","id":"d2","choices":[3,1]}
< {"type":"text","text":"You keep: A bent nail, A torn piece of cloth."}
< {"type":"text","text":"The nail might open the lock."}
< {"type":"items","items":[{"id":"guard","label":"sleepy guard"},{"id":"scratches","label":"scratches on the wall"},{"id":"lock","label":"lock with letter wheels"},{"id":"straw","label":"flattened straw"}]}
> {"type":"click","item":"lock"}
< {"type":"text","text":"Four letter wheels. Someone scratched a riddle next to them:"}
< {"type":"ask","id":"d3","title":"I have bars but no drinks, and you are standing in me. What am I?","strict":true}
> {"type":"answer","id":"d3","text":"Cell"}
< {"type":"text","text":"The wheels click into place: C, E, L, L. The lock opens!"}
> {"type":"click","item":"scratches"}
< {"type":"text","text":"You roll 14: in the dim light the scratches turn into a map."}
< {"type":"image","text":"A map scratched into the wall. An X marks the end of the corridor.","url":"/packs/demo/assets/wall-map-clear.png"}
```

The demo's exchange is checked byte for byte by `TestDemoMatchesProtocolDoc` (the dice roll aside).

## Pack images

`GET /packs/<pack id>/assets/<file>` serves a pack's images (PNG, JPEG, GIF, WebP) with their checked media type, an `ETag`, `Cache-Control: public, max-age=300` and `Content-Security-Policy: default-src 'none'; sandbox`. Anything else under `/packs/` is 404.

When the server shuts down it closes connections with 1001 (going away); the client reconnects with backoff (1 s doubling to 30 s).

Audio messages and further widgets (map, slider) are added here as their tasks land.
