# Patterns for pack authors

Game mechanics are written in Lua by the pack, not in the engine. This page collects patterns the demo pack (`content/demo`) uses, ready to copy. Once packs can share a Lua module (`require`, T-0026), they move into a library.

## Solved rooms and Whisp, the guide

Sometimes a player needs to go back to a room, sometimes there is no reason to. A room can be marked **solved** for a player, with one of two ways to treat going back:

| How | Going back | Use it when |
| --- | --- | --- |
| `ask` | Whisp asks first ("Do you really want to get locked in again?"); "No, stay here" keeps the player where they are | Going back is allowed but probably a mistake |
| `locked` | Whisp says why not, and the player stays; hide the room's exit from the clickable items too | There is nothing left to do there |

The marks live in `state.player`, so every room of the pack sees them. Moving through `go` instead of `room.go` applies them:

```lua
-- whisp says a line as Whisp, the guide; whisp_ask asks Whisp's question
-- and returns the number of the answer.
function whisp(text)
  say("Whisp: " .. text)
end

function whisp_ask(question, options)
  return ui.dialog("Whisp: " .. question, options)
end

-- keep stores value for key in state.player. The server can refuse a write
-- when it is full; then the player hears so and keep returns false.
function keep(key, value)
  local ok = state.player.set(key, value)
  if not ok then
    say("(The game cannot remember that right now. Try again in a moment.)")
    return false
  end
  return true
end

-- solve marks room id (a room id of this pack, never typed text) as solved
-- for this player. how is "ask" (Whisp asks text before the player goes
-- back) or "locked" (Whisp says text and the player stays).
function solve(id, how, text)
  return keep("solved_" .. id, { how = how, text = text })
end

function solved(id)
  return state.player.get("solved_" .. id)
end

-- go moves the player to room id once the hook ends, unless the room is
-- solved and the player stays. Returns whether the player goes.
function go(id)
  local s = solved(id)
  if s and s.how == "locked" then
    whisp(s.text)
    return false
  end
  if s and s.how == "ask" then
    if whisp_ask(s.text, { "Yes, go back", "No, stay here" }) ~= 1 then
      whisp("Good. Onwards!")
      return false
    end
  end
  room.go(id)
  return true
end
```

Keys are built from the room id, so pass only room ids of your pack, never text the player typed: every different key counts toward the player's 500 keys, and a key over 64 bytes is a script error. `state.player.set` can be refused when the server or the player's network is full (it returns nil and the reason instead of failing), so change the story only once `keep` returned true. `state.player` works only inside hooks, not at a script's top level.

In the demo, getting out of the cell solves it with `ask`:

```lua
if solve("cell", "ask", "Do you really want to get locked in again?") then
  room.go("corridor")
end
```

and taking the key in the guard room solves the corridor with `locked`, and drops the corridor's door from the guard room's items:

```lua
solve("corridor", "locked", "You have everything you need from the corridor. The way out is the back door.")
```

`ui.dialog` may wait before `room.go`, never after it in the same hook ([lua-sandbox.md](lua-sandbox.md)), which is why `go` asks first and moves last. Whisp's lines are plain text for now; a guide voice the client shows apart from the story is planned in the engine (T-0027), and then only `whisp` and `whisp_ask` change.

## A countdown across rooms

A threat that runs while the player moves between rooms (the guard refilling his bottle) is one number in `state.player`. The room that starts it sets it, and every room counts it down before each action:

```lua
-- in the cell, when the guard leaves
if keep("guard_away", 10) then
  say("He stomps off down the corridor to refill his bottle.")
end

-- in every room the player can reach while he is away
function act()
  local away = state.player.get("guard_away") or 0
  if away == 0 then
    caught()
    return false
  end
  away = away - 1
  keep("guard_away", away)
  if away == 1 then
    say("Footsteps on the stairs: the guard is coming back up!")
  end
  return true
end
```

In the room where the countdown starts, nil means the guard is still at his post, so the cell checks for nil before it counts and clears the value (`keep("guard_away", nil)`) when he is back. The count is in actions, not time; real deadlines come with timers (T-0012).
