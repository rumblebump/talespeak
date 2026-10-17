# WebSocket protocol

The browser talks to the engine over one WebSocket at `/ws` on the same origin. Upgrades from another origin are refused.

Every message, in both directions, is one JSON object with a `type` field.

## Limits

| Limit | Value | When exceeded |
| --- | --- | --- |
| Open connections per server | 1000 | Upgrade refused with HTTP 503 |
| Open connections per client address | 100 | Upgrade refused with HTTP 503 |
| Message size from the client | 16 KiB | Connection closed (1009, message too big) |
| Messages per connection | 20 at once, then 10 per second | Connection closed (1008, too many messages) |
| Silence from the client | 2 minutes | Connection closed (1008, idle timeout); clients ping every 30 s |
| Binary frame, or text that is not one JSON object (including `null` and arrays) | | Connection closed (1003) |

The client address is the TCP peer, unless the peer is a trusted proxy: then it is the rightmost `X-Forwarded-For` entry that is not a trusted proxy. `TALESPEAK_TRUSTED_PROXIES` (comma-separated CIDR prefixes) sets the trusted proxies; the default is loopback and the private ranges, which covers Caddy in the same Docker network. Narrow it to the proxy's own address when untrusted clients share the private network with the engine; an unset or empty value means the default. The per-client limit stays well above a school class, because a class behind one NAT address shares it.

## Messages

| Direction | `type` | Fields | Meaning |
| --- | --- | --- | --- |
| server → client | `welcome` | `version` | Sent once after connecting; a new session starts in the pack's start room |
| client → server | `ping` | | Keep-alive |
| server → client | `pong` | | Answer to `ping` |
| server → client | `room` | `id`, `title`, `text` | The room the player is in |
| server → client | `text` | `text` | A line of story text |
| server → client | `dialog` | `id`, `title`, `options` (1 to 9 strings) | A question; the script waits for the answer |
| client → server | `choose` | `id`, `option` (1-based) | The answer to the open dialog |
| server → client | `error` | `message` | The client sent something the server does not understand, or a room script failed; the connection stays open |

A dialog's `id` is valid until it is answered: an answer with an older `id`, or an `option` out of range, gets an `error` and the dialog stays open. Text fields are plain text: clients show them with `textContent`, never as HTML.

Entering the demo room:

```text
< {"type":"welcome","version":"local"}
< {"type":"room","id":"cell","title":"A damp cell","text":"Stone walls, ..."}
< {"type":"text","text":"A guard leans against the bars, half asleep."}
< {"type":"dialog","id":"d1","title":"What do you do, traveler?","options":["Ask the guard for water","Search the straw","Wait quietly"]}
> {"type":"choose","id":"d1","option":2}
< {"type":"text","text":"Under the straw you find a bent nail. It might open the lock."}
```

When the server shuts down it closes connections with 1001 (going away); the client reconnects with backoff (1 s doubling to 30 s).

Widgets and audio messages are added here as their tasks land.
