# Lua sandbox

Room scripts and, later, rule packs run in [gopher-lua](https://github.com/yuin/gopher-lua) (Lua 5.1, pure Go). Each world has one Lua state. Every script gets its own globals; the libraries below are shared.

## What a script can use

| Available | Notes |
| --- | --- |
| Base functions | `assert`, `error`, `getmetatable`, `ipairs`, `next`, `pairs`, `pcall`, `rawequal`, `rawget`, `rawset`, `select`, `setmetatable`, `tonumber`, `tostring`, `type`, `unpack` (at most 8000 values), `xpcall`, `_G`, `_VERSION` |
| `string` | All of it except patterns: `string.find` needs `plain = true`; `match`, `gmatch` and `gsub` raise an error. `rep`, `format` and `byte` are size-limited |
| `table` | All of it; `table.concat` results are at most 1 MiB |
| `math` | All of it |
| `say(text)` | Sends a line to the player (at most 4000 bytes) |
| `room.describe()` | Sends the current room's title and description |
| `ui.dialog(title, options)` | Shows 1 to 9 options and waits for the player; returns the option's number and its label |

Not available: `io`, `os`, `debug`, `package`/`require`, `coroutine`, `channel`, `load`, `loadstring`, `dofile`, `loadfile`, `getfenv`, `setfenv`, `collectgarbage`, `newproxy`, `print`. A script cannot read files, open connections, run programs or load other code.

**Why no patterns:** gopher-lua matches Lua patterns with a backtracking matcher written in Go. The time limit only interrupts Lua code, not Go functions, and a crafted pattern takes exponential time. Patterns come back once there is a matcher with a step limit.

**Why no coroutine library:** hooks already run as coroutines, and `ui.dialog` must pause the hook itself, not a coroutine the script made.

## Hooks

A room script defines global functions the engine calls:

| Hook | When | Argument |
| --- | --- | --- |
| `on_enter(player)` | A player enters the room | `player.name` |

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

The top level of a script runs once when the world starts, for setup. `say`, `room.describe` and `ui.dialog` only work inside hooks.

## Limits

| Limit | Default | When exceeded |
| --- | --- | --- |
| Time per call | 100 ms | The call stops with "script ran too long" |
| Memory allocated per call | 32 MiB | The call stops with "script used too much memory" |
| Memory the world's scripts keep | 64 MiB | The call fails, and so does every later call until the world is restarted |
| Call depth | 200 | Script error (stack overflow) |
| Values on the Lua stack | 65536 | Script error (registry overflow) |
| Output per call | 100 `say` or `room.describe` calls | Script error |
| Script size | 256 KiB | The pack does not load |

A call is everything from starting or resuming a hook until it ends or waits in `ui.dialog`. The time a player takes to answer a dialog does not count.

How they are enforced:

- **Time:** each call runs with a deadline that gopher-lua checks before every instruction. `pcall` cannot swallow it for long: the next instruction outside the `pcall` fails again.
- **Memory per call:** Go has no per-goroutine allocation counter, so a watcher samples the process's allocated bytes every millisecond while a call runs. Allocations by other goroutines count too; at these budgets that only matters on a heavily loaded server, where the call then fails cleanly.
- **Memory kept:** after enough allocation since the last check, the engine walks everything reachable from the scripts' globals and adds up rough sizes.
- **Single expensive Go calls:** functions that could build a huge result in one call (`string.rep`, `string.format` widths, `table.concat`, `unpack`, `string.byte`) check sizes before they run, because the watcher cannot interrupt Go code.

When a call fails, the player sees "This room's script failed. Tell the game master." (or "hit its limits"). The log gets the room and the Lua error with its line.
