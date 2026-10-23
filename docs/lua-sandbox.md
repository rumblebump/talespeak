# Lua sandbox

Room scripts and, later, rule packs run in [gopher-lua](https://github.com/yuin/gopher-lua) (Lua 5.1, pure Go). Each world has one Lua state. Every script gets its own globals for every player, and `_G` is that table. The libraries below are shared and read-only: assigning to `string.rep` or `ui.dialog` is an error, and `getmetatable` on a library, a script's globals or a string returns `"locked"`.

## What a script can use

| Available | Notes |
| --- | --- |
| Base functions | `assert`, `error`, `getmetatable`, `ipairs`, `next`, `pairs`, `pcall`, `rawequal`, `rawget`, `rawset` (not on libraries), `select`, `setmetatable`, `tonumber`, `tostring`, `type`, `unpack` (at most 8000 values), `xpcall`, `_G`, `_VERSION` |
| `string` | `byte` (at most 8000 values), `char`, `find` (with `plain = true` only), `format`, `len`, `lower`, `rep`, `reverse`, `sub`, `upper`. `match`, `gmatch`, `gfind` and `gsub` raise an error. `format` takes Lua 5.1's conversions only (`%c %d %i %o %u %x %X %e %E %f %g %G %q %s %%`, widths and precisions of at most two digits); `%s` takes strings and numbers |
| `table` | `concat`, `getn`, `insert`, `maxn`, `remove`, `sort` (at most 20000 entries) |
| `math` | All of it |
| `require(name)` | Returns what the pack's `lib/<name>.lua` returns (true when it returns nothing). The module runs the first time a room script's instance asks for it, with that instance's globals and within the running call's time and memory; later calls return the same value. Every room's instance, and every player, runs it separately, so a module keeps no state between rooms (that is `state.player`). Usually called at the top of a script: `local rooms = require("rooms")`. A module required there also runs at world start, for no player, so its top level must not call `say`, `ui` or `state.player` (put those in its functions). A module shares the room script's globals, so the globals it sets (and any hook it defines) are the room script's too. A missing module, an error in the module, or a module that requires itself (directly or through others) is a script error. A failed `require` keeps no result and runs the module again next time, but the globals it set before the error stay |
| `say(text [, {speak = true, show = false, from = "Whisp"}])` | Sends a line to the player (at most 4000 bytes). `speak = true` reads it aloud even when the player has not turned read aloud on; `show = false` (needs `speak`) only reads it aloud, for listening exercises, and shows it when the device has no local voice. The text still reaches the player's browser (speech runs there), so `show = false` is not for secrets such as the answer to a graded question. `from` names who says the line, such as the guide: 1 to 40 bytes on one line. The client shows the name with the line, styled apart from the story, and reads it in another local voice |
| `room.describe()` | Sends the current room's title, description and clickable items |
| `room.players()` | The players in the caller's room, the caller included, in the order they came in: `{{id = "p1", name = "traveler", gm = false}, ...}` (`gm` as `player.gm`). A player who comes back into a room is last again. A player who is leaving the world (in `on_leave`) is no longer listed |
| `world.players()` | Every player online in the world, in all rooms, the caller included, in the order they joined: `{{id = "p1", name = "traveler", room = "cell", gm = false}, ...}`. A player who connects again is last. A player who is leaving the world (in `on_leave`) is no longer listed. It lists only who is connected now, so it is right after a restart, where `on_leave` did not run: count the party with it rather than with `on_enter` and `on_leave` in `state.world`. Show other players' rooms, or whether a GM is about, only when the game needs it: in a class, that is where everyone is |
| `room.go(id)` | Moves the player to room `id` of the pack once the hook ends: the clickable items become the new room's, and its `on_enter` runs (a room without a script is described). The last `room.go` that succeeds wins, and `room.go` to the current room runs its `on_enter` again. An unknown id is a script error, and so is waiting in `ui.dialog`, `ui.select` or `ui.ask` after `room.go` in the same hook. No one moves when the hook fails, when the new room's script cannot start (the world or the player's network is full), or when the player's address has used up its Lua time before the new room's `on_enter`; the player stays and gets an error. The player keeps their instances of the last 4 rooms they entered, so coming back to one finds its globals as they were left; an older room starts over, so progress that must last goes in `state.player`. One player message moves through at most 4 rooms |
| `state.player.get(key)` | A copy of the value the player's scripts set for `key` (1 to 64 bytes), or nil. Every room the player visits sees the same values; other players never do. Both calls work in hooks only: a room's top level runs for no player, so calling them there fails the script at load. Metatables are not kept |
| `state.player.set(key, value)` | Keeps a copy of `value` for `key`; nil removes it. Values are nil, booleans, finite numbers, strings and tables of them with string or number keys, nested at most 8 deep. Returns true, or nil and the reason when a limit refuses the write (see below); a value that can never be kept, like a function, is a script error. Changing a table after `set` changes nothing until the next `set`. A value set before the hook fails stays set, like a global. The values last: they are saved after every hook and come back when the player returns, also after a restart ([accounts.md](accounts.md)) |
| `state.world.get(key)`, `state.world.set(key, value)` | Like `state.player`, but shared by every player of the world: a door one player opens is open for everyone. The other players' current rooms get `on_change` (see Hooks). It is saved after every hook and lasts as long as the world, through restarts |
| `state.world.update(key, fn)` | Sets `key` to `fn(value)`, where `value` is a copy of what `key` holds now: no other hook runs in between, and `fn` cannot ask a question or call `update` again, so two players taking the last potion never both get it. Returns what `set` returns. An error in `fn` is an error of the hook, and `key` keeps its value |
| `timer.start(name, seconds [, {show = "Guards arrive", room = true}])` | Starts the player's timer `name` (like an item id), which runs out after 1 to 86400 seconds (fractions allowed) and then calls `on_timer` in the room the player is in by then; starting it again replaces it. With `show`, the player sees the label with a countdown (one line of 1 to 100 bytes); without it, the timer is secret. At most 10 timers per player; an 11th returns nil and the reason. Timers last for the session for now; T-0012 keeps them through restarts. With `room = true` it is a **room timer** instead (below) |
| `timer.cancel(name [, {room = true}])`, `timer.left(name [, {room = true}])` | `cancel` stops the timer and returns whether it was running; `left` returns the seconds until it runs out, or nil. With `room = true`, of the room's timer `name` |
| `ui.dialog(title, options [, {from = "Whisp", timeout = 30}])` | Shows 1 to 9 options and waits for the player; returns the option's number and its label. `from` works as in `say`. With `timeout` (1 to 86400 seconds) the player sees a countdown; once it passes the question closes and returns nil, and the script decides what happens: `local n = ui.dialog("Fight or flee?", {"Fight", "Flee"}, {timeout = 30}) or 2` |
| `ui.select(title, options [, {min = 1, max = #options, from = "Whisp", timeout = 30}])` | Multiple choice: the player picks `min` to `max` of 1 to 9 options; returns two lists, the numbers picked (ascending) and their labels, or nil after the `timeout`. `from` works as in `say` |
| `ui.ask(question [, {strict = true, speak = true, show = false, from = "Whisp", timeout = 30}])` | Waits for a typed answer and returns it: one line of at most 500 bytes, trimmed, control characters turned into spaces; nil after the `timeout`. `strict = true` gives no typing help (no autocorrect, autocomplete or suggestions, clickable items hidden), for spelling practice and riddles. `speak` and `show` as in `say`, for the question. `from` works as in `say` |
| `ui.items({{id = "straw", label = "heap of straw"}, ...})` | Replaces what the player can click (at most 20; ids like room ids, labels 1 to 100 bytes); `{}` clears them. A click runs `on_click` |
| `ui.image(name [, caption])` | Shows an image from the pack's `assets/` folder as a popup, with a caption of at most 200 bytes; an unknown name is an error |
| `words.normalize(s)`, `words.same(a, b)` | Lowercase, trim and collapse spaces; `same` compares two answers that way, the exact check for spelling |

A list keeps at most 262143 entries (2^18 - 1) in its array part: `table.insert` refuses more, and an integer key past that goes to the table's hash part, where `#` does not count it. Without this, `t[6e7] = true` would fill 60 million empty slots in one step no limit can stop.

No string is ever longer than 1 MiB: `..`, `string.rep`, `string.format` and `table.concat` refuse to build one. Numbers passed as counts or positions must be within ±2^31. Varargs (`...`) are not available.

Not available: `io`, `os`, `debug`, `package` (the sandbox's `require` loads only the pack's own modules), `coroutine`, `channel`, `load`, `loadstring`, `dofile`, `loadfile`, `getfenv`, `setfenv`, `collectgarbage`, `newproxy`, `print`. A script cannot read files, open connections, run programs or load other code.

**Why no patterns:** gopher-lua matches Lua patterns with a backtracking matcher written in Go. The time limit only interrupts Lua code, not Go functions, and a crafted pattern takes exponential time. Patterns come back once there is a matcher with a step limit.

**Why no coroutine library:** not needed yet, and every hook already waits in `ui.dialog` on its own.

## Hooks

A room script defines global functions the engine calls:

| Hook | When | Argument |
| --- | --- | --- |
| `on_enter(player)` | A player enters the room | `player.name`, and in every hook `player.room`, the current room's id |
| `on_input(player, text)` | The player typed a line (outside a question) | the line, cleaned as for `ui.ask` |
| `on_click(player, id)` | The player clicked an item they were shown | the item's id |
| `on_timer(player, name)` | One of the player's timers ran out; runs in the room the player is in then | the timer's name |
| `on_change(player, keys)` | Another player's hook (or timer) changed `state.world`; runs in the room the player is in | a list of the changed keys, at most 50, the latest when more changed while the player waited |
| `on_leave(player)` | The player leaves the world (closes the game or loses the connection); runs in the room they are in | none |
| `on_command(player, name, args)` | The player typed one of the room's commands (`/name args`, see below), outside a question | the command's name, in lower case, and the rest of the line, trimmed |
| `on_reset(player)` | A game master typed `/reset room` for the room the player is in, before the room starts over ([gm-tools.md](gm-tools.md)): clear what the pack keeps of the room in `state.player` and `state.world`. It cannot ask or move the player, and runs even when the player's address used up its Lua time | none |

Every hook's `player` has `player.id`, the id of the player's character in the world (`"p17"`), which is never given to another player, also after a restart, and `player.name`, their account name. Keep `player.id` to know who took or did something, and use names for showing only. (A server without a database, in tests and local tools, names players `traveler`, `traveler 2`, ... with ids from `p1`.)

`player.gm` is true when the player is a game master of this world (they created it, or an admin made them one) and false otherwise; an admin who is not a member of the world is not its GM. The server reads it when the player connects; when an admin (or a game master's join code) changes the role of a player who is playing, their game connects again at once with the new role, so `player.gm` is never stale. A pack can leave a decision to the GM, for example whether a smaller party carries on:

```lua
function on_enter(player)
  local online = world.players()
  if #online < 3 and player.gm then
    local n = ui.dialog("Only " .. #online .. " detectives are here. Carry on?", {"Carry on", "Wait for the others"})
    if n == 1 then state.world.set("party", #online) end
  end
end
```

`on_timer` gets a third argument, `"room"`, when a room timer ran out (below), and nil for the player's own timers.

**`on_leave`** runs as the player goes, within their Lua time (when it is used up, it does not run). It can change `state.world`, so the other players hear of it in `on_change` (a turn passes on, an item goes back), and start room timers. Its output reaches no one, it cannot ask a question (`ui.dialog` raises an error), and `room.go` in it moves no one. It does not run when the server stops or crashes, or when the world unloads (only an empty world does); when the world is ended from the lobby, it runs for every player in no set order. So who is online is `world.players()`, not a list kept with `on_leave`.

**Room timers** (`timer.start(name, seconds, {room = true, show = "Mira's turn"})`) belong to the room, not to a player: one timer per name per room, at most 10 per room. Every player in the room sees a shown one, players coming in see it with the time left, and players moving out stop seeing it. Changes made in one hook reach the others once the hook ends, one message per timer however often it changed. When it runs out, `on_timer(player, name, "room")` runs once: for the player who started it while they are still in the room, otherwise for the player who has been there longest; in an empty room it is dropped. A room timer started where `state.world` is read-only (in `on_change` and what it starts) stays read-only. Room timers last as long as the world runs.

```lua
function on_enter(player)
  room.describe()
  local n, label = ui.dialog("What do you do, " .. player.name .. "?", {
    "Search the straw",
    "Wait",
  })
  say("You chose: " .. label)
end
```

While a hook waits in `ui.dialog`, `ui.select` or `ui.ask`, the player's typed lines and clicks are refused until they answer.

`on_timer` and `on_change` are not started by the player, so their output arrives on its own, between the player's actions. They count toward the player's script time like any hook; when it is used up they run a little later (and `on_change` then gets every key that changed meanwhile). When the player has a question open, they still run, but cannot ask another one; `room.go` from them closes the open question. `on_change` never asks a question, and it cannot change `state.world` (`set` and `update` return nil and the reason); neither can the `on_enter` of a room it moves the player to, or the `on_timer` of a timer it starts. So a change cannot bounce between players by itself. Writing the value a key already holds is not a change. A world delivers `on_change` at most every 250 ms: what changed meanwhile arrives in one call.

`state.world` can carry what one player typed to the other players (a pack that stores `on_input` text and says it in `on_change`). That is chat; the edu-mode chat filter and the GM transcript will cover it (T-0018, T-0011). A room that wants to show other players' changes reads `state.world` in `on_change`:

```lua
function on_change(player, keys)
  if state.world.get("gate") == "open" then
    say("Somewhere, a gate creaks open.")
    ui.items({{id = "gate", label = "open gate"}})
  end
end
```

```lua
function on_click(player, item)
  if item == "lock" then
    local answer = ui.ask("What word opens the lock?", { strict = true })
    if words.same(answer, "cell") then
      say("The lock opens!")
    end
  elseif item == "scratches" then
    -- the picture depends on a dice roll
    if math.random(1, 20) >= 10 then
      ui.image("wall-map-clear.png", "A map scratched into the wall.")
    else
      ui.image("wall-map-faint.png", "Faint scratches.")
    end
  end
end
```

**How `state.player` measures a value.** Sizes are those of the encoding the server keeps, the same one T-0018 stores, so a value that fits now fits later:

| Value | Bytes |
| --- | --- |
| `true`, `false` | 1 |
| A whole number up to 2^53 | 2 for -64 to 63, then 1 more for each 7 bits, at most 9 |
| Any other number | 9 |
| A string of n bytes (any bytes, kept exactly) | n + 2 up to 127 bytes, n + 3 up to 16383 |
| A table | 2, plus its keys and values |

The tables of one value may have at most 65536 slots in all, empty ones included, so a very sparse table (one with a few entries at large indexes, or many removed keys) is refused.

**Script globals are per player and last a session.** Every player runs their own instance of each room script: the top level runs when the player first enters the room, and globals and top-level locals belong to that player alone. Nothing a player types or does reaches another player through script variables. They are gone when the player disconnects. Progress the player's rooms share goes in `state.player`; state the world's players share on purpose goes in `state.world`. Both are kept in the database and last; globals never do, so nothing is shared or kept by accident.

**Checking answers:** `words.same` ignores only case and extra spaces. A check that forgives small typos comes with Whisp (T-0005).

The top level of a script runs once when the world starts, for setup. `say`, `room.describe` and the `ui` functions only work inside hooks. `ui.dialog`, `ui.select` and `ui.ask` can wait anywhere in a hook, also inside `pcall`, a `table.sort` comparator or a metamethod. When the player leaves while a hook waits, `ui.dialog` raises the error "the player left" and the hook runs to its end with its output dropped, within 5 ms; it cannot wait again.

The name `__concat` is reserved: every `..` in a script is compiled into a call to it.

## Commands

A room offers slash commands with a global table `commands`: each key is a command's name (1 to 20 of `a-z`, `0-9`, `_`, `-`), each value a table with an optional `help` (one line of at most 100 bytes, shown by `/help`) and `gm = true` for a command only the world's game masters may use. The engine lists them in `/help`, refuses a `gm` command to everyone else as if it did not exist, writes every use of one to the audit log, and shows the other players in the room that the game master used it. A typed `/name args` then runs `on_command(player, name, args)`. At most 20 commands count, and the engine looks at no more than 80 entries of the table. The engine's own commands (`help`, `players`, `rooms`, `go`, `say`, `reset`, `audit`) cannot be replaced.

```lua
local round = require("round")
commands = round.commands -- skip, turns, free: gm = true

function on_command(player, name, args)
  round.command(player, name, args)
end
```

The demo's `lib/round.lua` has the game master's turn commands: `/skip` ends the turn of whoever is acting, `/turns` starts turns for everyone in the room (names after it, separated by commas or spaces, set the order), `/free` lets everyone act at once.

## Limits

| Limit | Default | When exceeded |
| --- | --- | --- |
| Time per call | 100 ms | The call stops with "script ran too long" |
| Memory allocated per call | 32 MiB | The call stops with "script used too much memory" |
| Memory one player's instance of a script keeps, with what its `require`d modules return | 4 MiB | The call fails; the player's state in that room starts over on their next action |
| Memory the instances of one client address keep together | 64 MiB | New players and rooms from that address are refused ("Too many players from your network") until some leave |
| Memory all instances of a world keep together | 256 MiB | The call fails, and new players and rooms are refused ("This world is full right now") until others leave |
| Call depth | 200 | Script error (stack overflow) |
| Values on the Lua stack | 65536 | Script error (registry overflow) |
| Output per call | 100 calls of `say`, `room.describe`, `room.go`, `ui.items` and `ui.image` (questions do not count) | Script error |
| Script size | 256 KiB, for a room script and for a module | The pack does not load |
| `state.player` | 64-byte keys, 16 KiB per value, 256 KiB and 500 keys per player (key plus encoded value, see below), 50 `set` calls per hook, its dialogs included; it counts toward the memory of the player's address and the world, together with the room instances | `set` returns nil and the reason |
| `state.world` | As `state.player`, with 4 MiB and 5000 keys per world; its `set` and `update` calls count toward the same 50 writes per hook; it counts toward the world's memory | `set` and `update` return nil and the reason |
| Timers | 10 per player, 1 second to 1 day; question timeouts 1 second to 1 day | `timer.start` returns nil and the reason; a bad duration is a script error |

Time per call and the memory limits can be changed by the admin ([admin-limits.md](admin-limits.md)); the others are part of the pack format.

A call is everything from starting or resuming a hook until it ends or waits in `ui.dialog`. The time a player takes to answer a dialog does not count.

How they are enforced:

- **Time:** each call runs with a deadline that gopher-lua checks before every instruction. `pcall` cannot swallow it for long: the next instruction outside the `pcall` fails again. Go functions are not interrupted, so every library function a script gets finishes quickly whatever its arguments; that is why patterns are off and why sizes are checked before work starts.
- **Memory per call:** Go has no per-goroutine allocation counter, so a watcher samples the process's allocated bytes every millisecond while a call runs. Allocations by other goroutines count too. A story hook usually runs well under a millisecond, so for it to fail, other goroutines would have to allocate 32 MiB within that time; a hook that runs for tens of milliseconds is more exposed on a busy server, and then fails cleanly.
- **Memory kept:** every player's instance is measured on its own. After an instance's calls allocated an eighth of its limit since the last check, the engine walks everything reachable from its globals and from the locals and temporaries of its hooks waiting in `ui.dialog` (not the shared libraries), and adds up sizes estimated on the high side. The world's total is the sum of the last measurements plus what was allocated since, so it is never too low; only when that sum passes the world's limit are the instances that ran measured again. `string.sub` copies its result, so a short slice never keeps a long string alive unseen. Varargs are off because gopher-lua keeps them where the walk cannot see them.
- **What a table keeps:** the walk counts every slot of a table's array part, empty ones included, and every key gopher-lua remembers, removed ones included, with what those keys hold: gopher-lua never shrinks a table, so `t[k] = nil` frees nothing until the table itself is gone. Filling an array and clearing it, or adding and removing long string keys, therefore counts toward the limit as long as the table lives.
- **Sorting:** `table.sort` without a comparator runs in Go, so it takes at most 20000 entries (a few milliseconds).
- **No single Go call builds a huge result:** `..`, `string.rep`, `string.format`, `table.concat`, `unpack` and `string.byte` check sizes before they run.

When a call fails, the player sees "This room's script failed. Tell the game master." (or "hit its limits"). The log gets the room and the Lua error with its line.

## How hooks wait

Each hook runs on its own goroutine with its own Lua thread. `ui.dialog` blocks that goroutine until the player answers, then gives the hook a fresh time and memory budget. Lua only runs while the world's lock is held, one hook at a time, so the Lua state is never used by two goroutines at once.

## Known gaps

- Compiling a script is not limited: a large script with deep nesting can take about 100 ms and tens of MiB once, when the world starts. Scripts are limited to 256 KiB.
- A script's top level runs again for every player who enters its room, and each player's instance keeps its own copy of what it sets up, so a long word list costs memory per player and room (see the memory limits above). Data shared read-only between players is planned with T-0018.
- One world runs one hook at a time. A slow hook (up to the 100 ms limit) delays the other players of that world.
- A Go library function waiting with its caller (a comparator of `table.sort` waiting in `ui.dialog`) holds its own small state outside the estimate.

