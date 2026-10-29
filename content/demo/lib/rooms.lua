-- Helpers every room of the demo uses: Whisp the guide, the guard's
-- countdown, solved rooms and the code of the key box. A room script
-- loads them with require("rooms"); the module shares the script's
-- globals, so the functions below become the script's own. The values
-- live in state.player, so every room sees the same ones; the box code
-- lives in state.world, so every player of the world sees the same one.
--
-- Only definitions here (and lib/death.lua): a module required at a script's top
-- level also runs at world start, for no player, where say, ui and
-- state.player do not work.

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

-- guard_away is the number of actions until the guard is back, or nil
-- while he is at his post.
function guard_away()
  return state.player.get("guard_away")
end

function set_guard_away(n)
  return keep("guard_away", n)
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

-- box_code is the code of the key box in the guard room: three digits as
-- a string, like "417". Every world picks its own the first time a player
-- needs it, so a new world has a new code, and keeps it in state.world, so
-- the note under the stone and the box agree for every player of the
-- world. Returns nil when the world cannot keep it; the player hears so.
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

-- spelled is a code with spaces between its digits, as on the note:
-- "417" is "4 1 7".
function spelled(code)
  return string.sub(code, 1, 1) .. " " .. string.sub(code, 2, 2) .. " " .. string.sub(code, 3, 3)
end

-- Dying (lib/death.lua): the guard catching the player kills them, as the
-- world's death setting says. The cell is the save point: respawn wakes
-- the player up there with the guard back at his post. me is the player
-- whose script this is, set by every room's on_enter (each player runs
-- their own copy of the room).
death = require("death")
death.texts.respawn = "You wake up on the straw of your cell. Was it a dream? The guard is back at his post."

-- die kills the player after the room said how; it returns true when the
-- story is over for them here (stay or revive), false when they wake up
-- in the cell.
function die()
  if death.die(me) == "respawn" then
    set_guard_away(nil)
    state.player.set("solved_cell", nil)
    state.player.set("solved_corridor", nil)
    return false
  end
  return true
end

-- The game master brings a dead player back with /revive NAME, when the
-- world's death setting is revive (a pack could use a spell instead).
commands = { revive = { help = "bring a dead player back to life", gm = true } }

function on_command(player, name, args)
  if name ~= "revive" then
    return
  end
  for _, p in ipairs(world.players()) do
    if string.lower(p.name) == string.lower(args) then
      local ok, err = death.revive(p.id)
      if ok then
        say(p.name .. " lives again.")
      else
        say("You cannot revive " .. p.name .. ": " .. tostring(err) .. ".")
      end
      return
    end
  end
  say("Type /revive and the name of a dead player who is online.")
end

-- on_change tells a player who was revived, and shows the room again.
function on_change(player, keys)
  death.on_change(player, keys)
end
