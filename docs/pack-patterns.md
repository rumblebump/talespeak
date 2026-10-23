# Patterns for pack authors

Game mechanics are written in Lua by the pack, not in the engine. This page collects patterns the demo pack (`content/demo`) uses, ready to copy.

Put helpers that several rooms use into a module, `lib/<name>.lua`, and load it at the top of each room script with `require("<name>")` ([content-format.md](content-format.md)). The demo keeps the helpers below in `content/demo/lib/rooms.lua`; each room script starts with `require("rooms")`. A module shares the script's globals, so its functions become the script's own. Its top level also runs at world start for no player, so keep `say`, `ui` and `state.player` inside functions.

## Solved rooms and Whisp, the guide

Sometimes a player needs to go back to a room, sometimes there is no reason to. A room can be marked **solved** for a player, with one of two ways to treat going back:

| How | Going back | Use it when |
| --- | --- | --- |
| `ask` | Whisp asks first ("Do you really want to get locked in again?"); "No, stay here" keeps the player where they are | Going back is allowed but probably a mistake |
| `locked` | Whisp says why not, and the player stays; hide the room's exit from the clickable items too | There is nothing left to do there |

The marks live in `state.player`, so every room of the pack sees them. Moving through `go` instead of `room.go` applies them (all of this is in the demo's `lib/rooms.lua`):

```lua
-- whisp says a line as Whisp, the guide; whisp_ask asks Whisp's question
-- and returns the number of the answer.
function whisp(text)
  say(text, { from = "Whisp" })
end

function whisp_ask(question, options)
  return ui.dialog(question, options, { from = "Whisp" })
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

`ui.dialog` may wait before `room.go`, never after it in the same hook ([lua-sandbox.md](lua-sandbox.md)), which is why `go` asks first and moves last. Whisp speaks with `from = "Whisp"`: the client shows the name with the line, styled apart from the story, and reads it in another voice.

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

In the room where the countdown starts, nil means the guard is still at his post, so the cell checks for nil before it counts and clears the value (`keep("guard_away", nil)`) when he is back. The count is in actions, not time; for a deadline in real time, use `timer.start` and `on_timer` ([lua-sandbox.md](lua-sandbox.md)).

## A secret every world picks

A code, a password or a hiding place that should differ from one game to the next is picked with `math.random` the first time a player needs it, and kept in `state.world`, so every player of the world (and every room) finds the same one. The demo's key box code is `box_code()` in `lib/rooms.lua`:

```lua
function box_code()
  local code = state.world.get("box_code")
  if code then
    return code
  end
  local new = string.format("%03d", math.random(0, 999))
  local ok = state.world.update("box_code", function(v)
    return v or new -- two players at once: the first code stays
  end)
  if not ok then
    say("(The game cannot remember that right now. Try again in a moment.)")
    return nil
  end
  return state.world.get("box_code")
end
```

Both the clue (the note under the corridor's stone) and the check (the box in the guard room) call it, so it does not matter which one a player reaches first. Pick it inside a hook, not at a script's top level: the top level also runs at world start, where there is no player. `math.random` is not for secrets that protect anything real: it only keeps a puzzle from being the same every time. For a code that should change per player instead, keep it in `state.player`.

## Rounds in turns

A room runs free (everyone acts at once) until a script starts turns: a set order, and only the player whose turn it is acts until they are done. `lib/round.lua` in the demo does it in Lua, on `state.world`, `room.players()`, room timers and `on_leave` ([lua-sandbox.md](lua-sandbox.md)); copy it into your pack's `lib/`. The demo's forest clearing (`scripts/clearing.lua`) plays dice with it.

```lua
local round = require("round")

-- start turns for everyone here, 30 seconds each
round.turns(player, nil, { seconds = 30 })

-- in every action that needs a turn
if not round.may_act(player) then
  return -- the library said "Wait for your turn: Mira is acting."
end

-- the player ends their turn (a "done" item, or typed)
round.done(player)

-- wire the library into the room's hooks
function on_change(player, keys) round.changed(player, keys) end
function on_timer(player, name, kind) round.timeout(player, name, kind) end
function on_leave(player) round.leave(player) end

-- and say what happens
function on_turn(player) say("Your turn!") end
function on_round_end(player, n) say("Round " .. n .. " is over.") end
```

| Function | What it does |
| --- | --- |
| `round.turns(player, order, {seconds = n})` | Starts turns in the player's room: `order` is a list of player ids (initiative rolls, say), or nil for everyone in the order they came in. Each turn gets a deadline, shown to the room as "Mira's turn" |
| `round.free(player)` | Everyone acts at once again |
| `round.current(player)` | The id and name of who acts, or nil |
| `round.may_act(player)` | True when the room is free or it is the player's turn; otherwise says who acts and returns false |
| `round.done(player)`, `round.next(player)` | The player ends their own turn; the script skips whoever acts |
| `round.leave(player)` | Takes the player out (call it before `room.go` out of the room, and in `on_leave`) |
| `round.status(player)` | A line like "Round 2. Order: Mira (acting), Tom (you)." |

`on_turn` runs in the instance of the player whose turn starts, usually from `on_change`, so it should only say things and set items: questions and world writes belong to the player's own clicks. A player who leaves is skipped; one who comes in during a round joins at the next. Without `on_turn_timeout(player, id, name)` a turn whose time ran out is skipped. The round lives in `state.world` as `round_<room id>`, so it survives a restart; the turn's deadline is a room timer, which does not yet (T-0012).

## Items players share

`lib/items.lua` gives each shared item a take rule, written in Lua on `state.world.update` so two players never both get the last one:

| Rule | What happens |
| --- | --- |
| `each` | Every player takes one, once |
| `once` | The first player has it; it is gone for everyone else |
| `count = N` | One each until N are gone |
| `split`, `amount = N` | The first take shares N among the players in the room then; each takes their share, the rest stays |

```lua
local items = require("items")
items.define({
  { id = "purse", rule = "split", amount = 12, gives = "silver" },
  { id = "horn", rule = "once" },
})

local got, why, who = items.take(player, "horn")
if got then
  say("You take the horn.")
elseif why == "gone" then
  say(who .. " has it already.")
end

-- show each player only what they can still take
ui.items(items.visible(player, ALL_ITEMS))
```

Call `items.visible` again in `on_change`: the other players' takes change the world keys `item_<id>`. `items.release(player, id)` in `on_leave` puts a `once` item back when its owner leaves.

**Effects that wear off:** `items.bless(player, "blessing", {seconds = 60, moves = 5, show = "Blessing"})` lasts a minute or five moves, whichever comes first. Call `items.move(player)` once per action and `items.on_timer(player, name)` in `on_timer`; `on_effect_end(player, id)` runs when it wears off. `items.blessed(player, id)` asks whether it is still on.

## Save spots for a party

A game in levels that a party plays together, saved at the end of each level, is written in Lua on `state.world` alone, so that one player's hook can move the whole party. The clockmaker pack (`content/clockmaker/lib/case.lua` and its parts `case_*.lua`) does it:

| World key | Holds |
| --- | --- |
| `case` | The level, a run number, the party (`members`, the join `order`, who is `active`), and whether the case is solved |
| `lv` | The progress of the current level; empty again at every save spot |
| `bag_<player id>`, `save_<player id>` | What each player carries, and their bag as it was when the level began. In `state.world`, not `state.player`, because a save spot saves everyone's bag at once |

- **Save spot:** a door at the end of the level. A player who clicks it waits there; when every active player waits, that hook copies every `bag_` to `save_`, clears `lv`, raises the level and the run, and moves the player to the next level's first room. The others are moved by `on_change`, which may call `room.go`.
- **Everyone left:** the `on_leave` of the last party member in `world.players()` starts the level again: `lv` cleared, every `bag_` set back to its `save_`, the run raised. A player whose `state.player` run is older is sent to the level's first room when they come back, also after a restart, because their character's room is saved too.
- **Party size:** puzzles read how many the level is made for (`lv.n`), fixed when the level starts, so a player joining later does not change a half-solved puzzle.
- **The lead decides:** going on without players who left the game (what they carried goes to the lead), or playing the level again with only those here. Both free those players' seats, so a fifth player waiting outside can join; a player who comes back later joins again when a seat is free. The lead is a game master of the world who is online and on the case (`player.gm`), else the first player online. Who is online is always `world.players()`, never a list kept with `on_enter` and `on_leave`, because `on_leave` does not run when the server stops.

A restart is not everyone leaving: after one, the level goes on where it was.

## One module in several files

A module that grows past about 500 lines can be split by function and still be one module to the rooms. The clockmaker's `case.lua` installs its parts at the end of its top level:

```lua
for _, name in ipairs({ "case_secrets", "case_bags", "case_spots", "case_input" }) do
  require(name)(case)
end
return case
```

Each part defines its functions on a table of its own whose `__index` is the main module, and returns the function that copies them there, so `case.x` inside a part finds everything:

```lua
local case, main = {}, nil
setmetatable(case, { __index = function(_, k) return main[k] end })

function case.bag(id) ... end

return function(m)
  main = m
  for k, f in pairs(case) do
    m[k] = f
  end
end
```

A part never requires the main module itself: a module that requires itself through others is a script error.
