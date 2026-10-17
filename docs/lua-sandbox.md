# Lua sandbox

Room scripts and, later, rule packs run in [gopher-lua](https://github.com/yuin/gopher-lua) (Lua 5.1, pure Go). Each world has one Lua state. Every script gets its own globals, and `_G` is that script's own table. The libraries below are shared and read-only: assigning to `string.rep` or `ui.dialog` is an error, and `getmetatable` on a library, a script's globals or a string returns `"locked"`.

## What a script can use

| Available | Notes |
| --- | --- |
| Base functions | `assert`, `error`, `getmetatable`, `ipairs`, `next`, `pairs`, `pcall`, `rawequal`, `rawget`, `rawset` (not on libraries), `select`, `setmetatable`, `tonumber`, `tostring`, `type`, `unpack` (at most 8000 values), `xpcall`, `_G`, `_VERSION` |
| `string` | `byte` (at most 8000 values), `char`, `find` (with `plain = true` only), `format`, `len`, `lower`, `rep`, `reverse`, `sub`, `upper`. `match`, `gmatch`, `gfind` and `gsub` raise an error. `format` takes Lua 5.1's conversions only (`%c %d %i %o %u %x %X %e %E %f %g %G %q %s %%`, widths and precisions of at most two digits); `%s` takes strings and numbers |
| `table` | `concat`, `getn`, `insert`, `maxn`, `remove`, `sort` (at most 20000 entries) |
| `math` | All of it |
| `say(text [, {speak = true, show = false}])` | Sends a line to the player (at most 4000 bytes). `speak = true` reads it aloud even when the player has not turned read aloud on; `show = false` (needs `speak`) only reads it aloud, for listening exercises, and shows it when the device has no local voice |
| `room.describe()` | Sends the current room's title, description and clickable items |
| `ui.dialog(title, options)` | Shows 1 to 9 options and waits for the player; returns the option's number and its label |
| `ui.select(title, options [, {min = 1, max = #options}])` | Multiple choice: the player picks `min` to `max` of 1 to 9 options; returns two lists, the numbers picked (ascending) and their labels |
| `ui.ask(question [, {strict = true, speak = true, show = false}])` | Waits for a typed answer and returns it: one line of at most 500 bytes, trimmed, control characters turned into spaces. `strict = true` gives no typing help (no autocorrect, autocomplete or suggestions, clickable items hidden), for spelling practice and riddles. `speak` and `show` as in `say`, for the question |
| `ui.items({{id = "straw", label = "heap of straw"}, ...})` | Replaces what the player can click (at most 20; ids like room ids, labels 1 to 100 bytes); `{}` clears them. A click runs `on_click` |
| `ui.image(name [, caption])` | Shows an image from the pack's `assets/` folder as a popup, with a caption of at most 200 bytes; an unknown name is an error |
| `words.normalize(s)`, `words.same(a, b)` | Lowercase, trim and collapse spaces; `same` compares two answers that way, the exact check for spelling |

No string is ever longer than 1 MiB: `..`, `string.rep`, `string.format` and `table.concat` refuse to build one. Numbers passed as counts or positions must be within ±2^31. Varargs (`...`) are not available.

Not available: `io`, `os`, `debug`, `package`/`require`, `coroutine`, `channel`, `load`, `loadstring`, `dofile`, `loadfile`, `getfenv`, `setfenv`, `collectgarbage`, `newproxy`, `print`. A script cannot read files, open connections, run programs or load other code.

**Why no patterns:** gopher-lua matches Lua patterns with a backtracking matcher written in Go. The time limit only interrupts Lua code, not Go functions, and a crafted pattern takes exponential time. Patterns come back once there is a matcher with a step limit.

**Why no coroutine library:** not needed yet, and every hook already waits in `ui.dialog` on its own.

## Hooks

A room script defines global functions the engine calls:

| Hook | When | Argument |
| --- | --- | --- |
| `on_enter(player)` | A player enters the room | `player.name` |
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

**Globals belong to the world.** Every player of a world runs the same script state: a global or top-level local set for one player is seen by all. Keep per-player progress out of globals until profiles store it (T-0010).

**Checking answers:** `words.same` ignores only case and extra spaces. A check that forgives small typos comes with Whisp (T-0005).

The top level of a script runs once when the world starts, for setup. `say`, `room.describe` and the `ui` functions only work inside hooks. `ui.dialog`, `ui.select` and `ui.ask` can wait anywhere in a hook, also inside `pcall`, a `table.sort` comparator or a metamethod. When the player leaves while a hook waits, `ui.dialog` raises the error "the player left" and the hook runs to its end with its output dropped, within 5 ms; it cannot wait again.

The name `__concat` is reserved: every `..` in a script is compiled into a call to it.

## Limits

| Limit | Default | When exceeded |
| --- | --- | --- |
| Time per call | 100 ms | The call stops with "script ran too long" |
| Memory allocated per call | 32 MiB | The call stops with "script used too much memory" |
| Memory the world's scripts keep | 64 MiB | The call fails, and so does every later call until the world is restarted |
| Call depth | 200 | Script error (stack overflow) |
| Values on the Lua stack | 65536 | Script error (registry overflow) |
| Output per call | 100 calls of `say`, `room.describe`, `ui.items` and `ui.image` (questions do not count) | Script error |
| Script size | 256 KiB | The pack does not load |

Time per call and both memory limits can be changed by the admin ([admin-limits.md](admin-limits.md)); the others are part of the pack format.

A call is everything from starting or resuming a hook until it ends or waits in `ui.dialog`. The time a player takes to answer a dialog does not count.

How they are enforced:

- **Time:** each call runs with a deadline that gopher-lua checks before every instruction. `pcall` cannot swallow it for long: the next instruction outside the `pcall` fails again. Go functions are not interrupted, so every library function a script gets finishes quickly whatever its arguments; that is why patterns are off and why sizes are checked before work starts.
- **Memory per call:** Go has no per-goroutine allocation counter, so a watcher samples the process's allocated bytes every millisecond while a call runs. Allocations by other goroutines count too. A story hook usually runs well under a millisecond, so for it to fail, other goroutines would have to allocate 32 MiB within that time; a hook that runs for tens of milliseconds is more exposed on a busy server, and then fails cleanly.
- **Memory kept:** after enough allocation since the last check, the engine walks everything reachable from the scripts' globals and from the locals and temporaries of hooks waiting in `ui.dialog`, and adds up sizes estimated on the high side. `string.sub` copies its result, so a short slice never keeps a long string alive unseen. Varargs are off because gopher-lua keeps them where the walk cannot see them.
- **Sorting:** `table.sort` without a comparator runs in Go, so it takes at most 20000 entries (a few milliseconds).
- **No single Go call builds a huge result:** `..`, `string.rep`, `string.format`, `table.concat`, `unpack` and `string.byte` check sizes before they run.

When a call fails, the player sees "This room's script failed. Tell the game master." (or "hit its limits"). The log gets the room and the Lua error with its line.

## How hooks wait

Each hook runs on its own goroutine with its own Lua thread. `ui.dialog` blocks that goroutine until the player answers, then gives the hook a fresh time and memory budget. Lua only runs while the world's lock is held, one hook at a time, so the Lua state is never used by two goroutines at once.

## Known gaps

- Compiling a script is not limited: a large script with deep nesting can take about 100 ms and tens of MiB once, when the world starts. Scripts are limited to 256 KiB.
- One world runs one hook at a time. A slow hook (up to the 100 ms limit) delays the other players of that world.
- A Go library function waiting with its caller (a comparator of `table.sort` waiting in `ui.dialog`) holds its own small state outside the estimate.

