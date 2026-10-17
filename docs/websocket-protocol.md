# WebSocket protocol

The browser talks to the engine over one WebSocket at `/ws` on the same origin. Upgrades from another origin are refused.

Every message, in both directions, is one JSON object with a `type` field.

## Limits

| Limit | Value | When exceeded |
| --- | --- | --- |
| Open connections per server | 1000 | Upgrade refused with HTTP 503 |
| Message size from the client | 16 KiB | Connection closed (1009, message too big) |
| Messages per connection | 20 at once, then 10 per second | Connection closed (1008, too many messages) |
| Silence from the client | 2 minutes | Connection closed (1008, idle timeout); clients ping every 30 s |
| Binary frame, or text that is not one JSON object (including `null` and arrays) | | Connection closed (1003) |

## Messages

| Direction | `type` | Fields | Meaning |
| --- | --- | --- | --- |
| server → client | `welcome` | `version` | Sent once after connecting |
| client → server | `ping` | | Keep-alive |
| server → client | `pong` | | Answer to `ping` |
| server → client | `error` | `message` | The client sent something the server does not understand; the connection stays open |

When the server shuts down it closes connections with 1001 (going away); the client reconnects with backoff (1 s doubling to 30 s).

Rooms, dialogs, widgets and audio messages are added here as their tasks land (T-0003, T-0004).
