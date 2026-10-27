# Game master tools

A world's game master (GM) runs the game for its players: the account that created the world, or one an admin (or, outside edu mode, the world's GM) made GM ([accounts.md](accounts.md)). In edu mode only admins make game masters, so they are the teachers. GM tools are typed into the game as slash commands; a player who is no GM gets the same answer for them as for a command that does not exist, so the tools stay out of sight.

## Roles

| Role | Where | May |
| --- | --- | --- |
| player | every account, in every world it plays | play; type `/help` and the room's commands for everyone |
| game master | per world (`world_members.role` `gm`) | everything a player may; in GM mode the commands below; make join codes, see the world's members and take players out of it, and end the world from the lobby; outside edu mode also make the world's players GMs |
| admin | the whole server (`accounts.role` `admin`) | see and end every world, make game masters and players and add accounts by name (the lobby's **members** list, `POST /api/members`, or `talespeak invite -gm`); an admin is a world's GM only when they are its member as one |

In edu mode, worlds are only for their members, kids join only with a code from a teacher or an admin, and only admins make game masters. Packs never change who has which role: `player.gm` is read-only.

## GM mode

GM mode works like `sudo`. On a world's card a GM picks **play as GM** (GM mode on) or **play as player** (off; the WebSocket URL carries `as=player`), and switches in the game with `/gm on`, `/gm off` or `/gm`. With GM mode off, a GM plays like everyone else: no GM commands (they answer as unknown ones), `player.gm` is false, and so is their `gm` in `room.players` and `world.players`. Every switch is in the audit log, and the players in the room see it marked as the GM's ("acts as the game master now.", "plays as a player again."). A connection that drops and comes back starts in the mode picked on the card.

## The edu floor

What no pack, request or Lua lowers in edu mode, covered by `internal/account/edu_floor_test.go` and `internal/script/perm_test.go`:

- Only admins make game masters: a GM's join codes are for players, and the members list lets a GM take players out but make no GM and change no other GM. Registering grants nothing: a new account sees an empty lobby.
- Packs cannot give GM rights: `player.gm` is the engine's, a script that changes the field changes nothing the engine checks, and `player:can` answers the engine's permissions (`gm.tools`, `world.pause`, `timer.edit`, `chat.view_all`, `player.mute`, `item.spawn`) only for a GM in GM mode, whatever the pack defines ([lua-sandbox.md](lua-sandbox.md#permissions)).
- Still to come with chat (T-0006): GMs see all chat, no private chat outside the world, and the notice that GMs can read chat.

## Commands

| Command | Who | What it does |
| --- | --- | --- |
| `/help` | everyone | Lists what the player may type: these commands, and the commands of the room they are in |
| `/gm [on\|off]` | GM, in GM mode or not | Switches GM mode on or off; `/gm` alone switches it over |
| `/players` | GM | Who is online, in which room, and who is GM |
| `/rooms` | GM | The pack's rooms with their ids |
| `/go ROOM` | GM | Goes to a room (by its id from `/rooms`) yourself, as `room.go` would; its `on_enter` runs |
| `/say TEXT` | GM | Every player online in the world reads TEXT, marked as the game master's |
| `/reset room [ROOM]` | GM | Starts the room you are in (or ROOM) over for every player in it: questions they had open close and the room's question deadlines are forgotten, the room's timers stop (also those waiting for a player), the pack's `on_reset` clears what it keeps of the room, the players' instances of the room's script start over and `on_enter` runs again (for a player out of Lua time, as when joining, only with their next line). Players elsewhere keep what they have |
| `/audit` | GM | The last 20 entries of the world's audit log, the latest first |
| `/pause`, `/resume` | GM | Stops the game for everyone and goes on (below) |
| `/timers` | GM | The timers that run in the world, numbered: the rooms' timers, then each online player's timers and open question with a time limit, with label, name and time left (at most 50) |
| `/extend N TIME` | GM | Gives timer N of the last `/timers` list TIME more (`90`, `30s`, `5m`, `1m30s`, `1h`; at most a day in all). A timer that ran out and waits gets TIME from now |
| `/cancel N` | GM | Stops timer N of the last `/timers` list; its `on_timer` does not run. For a question, the time limit goes and the question waits for its answer |
| `/extratime [NAME FACTOR]` | GM | Gives a member of the world 1 to 3 times as long (`/extratime ann 1.5`); alone, lists the factors of the players online |
| `/mintime [TIME\|off]` | GM | The world's minimum time (off by default): questions and shown timers shorter than that get that long, up to an hour (`/mintime 30s`); alone, says what it is |
| a room's own | as the pack says | Packs add commands with a `commands` table and `on_command` ([lua-sandbox.md](lua-sandbox.md#commands)); the demo's clearing has `/skip`, `/turns` and `/free` for turns |

Command names work in any letter case. The engine's commands also work while a question is open; a room's commands wait until it is answered.

`/say`, `/reset`, `/gm`, `/pause`, `/resume`, `/extend`, `/cancel` and a room's GM commands reach other players, so a GM may send five of them at once and then one a second. The count belongs to the GM's account, so connecting again does not refill it. When a GM resets a room, the Lua time of every player's `on_reset` is charged to the GM's connection.

## Time: pause, timers and extra time

`/pause` stops the world's clock: every timer, room timer and question time limit stands still with the time it has left, also the timers of players who are away, and a paused world stays paused through a restart until a GM types `/resume`. Every player sees "paused the game" with the GM mark and a line that the game is paused, and their countdowns stop. While it is paused, what players type, click and answer, and the room's commands, get "The game is paused." and do nothing; `/help` works. A GM in GM mode still plays and acts (a GM playing as a player waits too). On `/resume` every countdown comes back with its time left. Game-time timers ([lua-sandbox.md](lua-sandbox.md)) stand still while the world is paused and also while nobody is online.

`/timers` numbers the timers, and `/extend` and `/cancel` take a number from the last list the GM saw. A number whose timer ran out, stopped or was started again since is refused ("/timers lists them again"), so an old list never hits another timer. The players affected see the countdown change and a GM mark ("gave 5m more: \"Guards arrive\".", "stopped a secret timer."). Timers of players who are away wait in the database and are not listed.

Extra time is a factor per member of the world, kept in `world_members.extra_time`: their question time limits and the timers shown to them that start from then on run that many times as long; a room timer takes the largest factor of the players in the room as it starts. `/mintime` is per world. Both leave secret timers alone, which packs use for their rules, and neither changes a timer that already runs (`/extend` does). With extra time and the minimum time, nothing runs longer than a day. For a kids' class, a teacher sets `/mintime 30s` once, and `/extratime` for a child who reads slowly.

## What players see

A GM's words and actions have a look a pack cannot copy: they come as `notice` messages ([websocket-protocol.md](websocket-protocol.md)), which the client shows in a box with a **GM** badge and the GM's name, whatever name a pack gives its own lines. When a GM resets a room or uses a room's GM command, the other players there see it marked the same way ("started this room over.", "used /skip anna"; past 60 bytes the arguments are cut, the audit log keeps them whole). With read aloud on, the client speaks a GM's line in the plain voice as "Game master NAME says: …", never in a pack's guide voice.

## The audit log

Every GM action is written to the world's audit log before it happens: `/say` with its text, `/go`, `/reset room`, `/gm` (`gm mode` on or off), `/pause`, `/resume`, `/extend` and `/cancel` with the timer and the time, `/extratime` with the name and factor, `/mintime`, a room's GM commands with what was typed after them, and every role change in the members list or `POST /api/members`. When the log cannot be written, the action does not happen and the GM is told so. The log keeps the last 1000 entries of each world and outlasts the world: ending it deletes the game state, not the log. GMs read it with `/audit`; admins also in the database (`audit_log`).

## Changing roles while playing

When an admin or a GM makes a player a GM (or a GM a player, or takes them out of the world), or a player joins with a game master's code, a game they have open in that world ends with "Your role in this world has changed." and connects again with the new role. Packs see the new `player.gm` from that connection on; a player who may no longer play the world goes back to the lobby.
