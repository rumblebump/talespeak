# Lua sandbox

Room scripts and, later, rule packs run in [gopher-lua](https://github.com/yuin/gopher-lua) (Lua 5.1, pure Go). Each world has one Lua state. Every script gets its own globals for every player, and `_G` is that table. The libraries below are shared and read-only: assigning to `string.rep` or `ui.dialog` is an error, and `getmetatable` on a library, a script's globals or a string returns `"locked"`.

## What a script can use

| Available | Notes |
| --- | --- |
| Base functions | `assert`, `error`, `getmetatable`, `ipairs`, `next`, `pairs`, `pcall`, `rawequal`, `rawget`, `rawset` (not on libraries), `select`, `setmetatable`, `tonumber`, `tostring`, `type`, `unpack` (at most 8000 values), `xpcall`, `_G`, `_VERSION` |
| `string` | `byte` (at most 8000 values), `char`, `find` (with `plain = true` only), `format`, `len`, `lower`, `rep`, `reverse`, `sub`, `upper`. `match`, `gmatch`, `gfind` and `gsub` raise an error. `format` takes Lua 5.1's conversions only (`%c %d %i %o %u %x %X %e %E %f %g %G %q %s %%`, widths and precisions of at most two digits); `%s` takes strings and numbers |
| `table` | `concat`, `getn`, `insert`, `maxn`, `remove`, `sort` (at most 20000 entries) |
| `math` | All of it |
| `require(name)` | Returns what the pack's `lib/<name>.lua` returns (true when it returns nothing). The module runs the first time a room script's instance asks for it, with that instance's globals and within the running call's time and memory; later calls return the same value. Every room's instance, and every player, runs it separately, so a module keeps no state between rooms (that is `state.player`). Usually called at the top of a script: `local rooms = require("rooms")`. A missing module, an error in the module, or a module that requires itself (directly or through others) is a script error, and a failed `require` runs the module again next time |
| `say(text [, {speak = true, show = false}])` | Sends a line to the player (at most 4000 bytes). `speak = true` reads it aloud even when the player has not turned read aloud on; `show = false` (needs `speak`) only reads it aloud, for listening exercises, and shows it when the device has no local voice. The text still reaches the player's browser (speech runs there), so `show = false` is not for secrets such as the answer to a graded question |
| `room.describe()` | Sends the current room's title, description and clickable items |
| `room.go(id)` | Moves the player to room `id` of the pack once the hook ends: the clickable items become the new room's, and its `on_enter` runs (a room without a script is described). The last `room.go` that succeeds wins, and `room.go` to the current room runs its `on_enter` again. An unknown id is a script error, and so is waiting in `ui.dialog`, `ui.select` or `ui.ask` after `room.go` in the same hook. No one moves when the hook fails, when the new room's script cannot start (the world or the player's network is full), or when the player's address has used up its Lua time before the new room's `on_enter`; the player stays and gets an error. The player keeps their instances of the last 4 rooms they entered, so coming back to one finds its globals as they were left; an older room starts over, so progress that must last goes in `state.player`. One player message moves through at most 4 rooms |
| `state.player.get(key)` | A copy of the value the player's scripts set for `key` (1 to 64 bytes), or nil. Every room the player visits sees the same values; other players never do. Both calls work in hooks only: a room's top level runs for no player, so calling them there fails the script at load. Metatables are not kept |
| `state.player.set(key, value)` | Keeps a copy of `value` for `key`; nil removes it. Values are nil, booleans, finite numbers, strings and tables of them with string or number keys, nested at most 8 deep. Returns true, or nil and the reason when a limit refuses the write (see below); a value that can never be kept, like a function, is a script error. Changing a table after `set` changes nothing until the next `set`. A value set before the hook fails stays set, like a global. For now the values last for the session; T-0018 keeps them in the database with the same calls and the same encoded sizes |
| `ui.dialog(title, options)` | Shows 1 to 9 options and waits for the player; returns the option's number and its label |
| `ui.select(title, options [, {min = 1, max = #options}])` | Multiple choice: the player picks `min` to `max` of 1 to 9 options; returns two lists, the numbers picked (ascending) and their labels |
| `ui.ask(question [, {strict = true, speak = true, show = false}])` | Waits for a typed answer and returns it: one line of at most 500 bytes, trimmed, control characters turned into spaces. `strict = true` gives no typing help (no autocorrect, autocomplete or suggestions, clickable items hidden), for spelling practice and riddles. `speak` and `show` as in `say`, for the question |
| `ui.items({{id = "straw", label = "heap of straw"}, ...})` | Replaces what the player can click (at most 20; ids like room ids, labels 1 to 100 bytes); `{}` clears them. A click runs `on_click` |
| `ui.image(name [, caption])` | Shows an image from the pack's `assets/` folder as a popup, with a caption of at most 200 bytes; an unknown name is an error |
| `words.normalize(s)`, `words.same(a, b)` | Lowercase, trim and collapse spaces; `same` compares two answers that way, the exact check for spelling |

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

**Script state is per player and lasts a session.** Every player runs their own instance of each room script: the top level runs when the player first enters the room, and globals and top-level locals belong to that player alone. Nothing a player types or does reaches another player through script variables. They are gone when the player disconnects. Progress the player's rooms share goes in `state.player`, which lasts the session for now; storing it in the database, and `state.world` for state a party shares on purpose, come with T-0018.

**Checking answers:** `words.same` ignores only case and extra spaces. A check that forgives small typos comes with Whisp (T-0005).

The top level of a script runs once when the world starts, for setup. `say`, `room.describe` and the `ui` functions only work inside hooks. `ui.dialog`, `ui.select` and `ui.ask` can wait anywhere in a hook, also inside `pcall`, a `table.sort` comparator or a metamethod. When the player leaves while a hook waits, `ui.dialog` raises the error "the player left" and the hook runs to its end with its output dropped, within 5 ms; it cannot wait again.

The name `__concat` is reserved: every `..` in a script is compiled into a call to it.

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

Time per call and the memory limits can be changed by the admin ([admin-limits.md](admin-limits.md)); the others are part of the pack format.

A call is everything from starting or resuming a hook until it ends or waits in `ui.dialog`. The time a player takes to answer a dialog does not count.

How they are enforced:

- **Time:** each call runs with a deadline that gopher-lua checks before every instruction. `pcall` cannot swallow it for long: the next instruction outside the `pcall` fails again. Go functions are not interrupted, so every library function a script gets finishes quickly whatever its arguments; that is why patterns are off and why sizes are checked before work starts.
- **Memory per call:** Go has no per-goroutine allocation counter, so a watcher samples the process's allocated bytes every millisecond while a call runs. Allocations by other goroutines count too. A story hook usually runs well under a millisecond, so for it to fail, other goroutines would have to allocate 32 MiB within that time; a hook that runs for tens of milliseconds is more exposed on a busy server, and then fails cleanly.
- **Memory kept:** every player's instance is measured on its own. After an instance's calls allocated an eighth of its limit since the last check, the engine walks everything reachable from its globals and from the locals and temporaries of its hooks waiting in `ui.dialog` (not the shared libraries), and adds up sizes estimated on the high side. The world's total is the sum of the last measurements plus what was allocated since, so it is never too low; only when that sum passes the world's limit are the instances that ran measured again. `string.sub` copies its result, so a short slice never keeps a long string alive unseen. Varargs are off because gopher-lua keeps them where the walk cannot see them.
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

