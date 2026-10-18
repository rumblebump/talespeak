-- Shared by the demo's rooms, which load it with require("rooms"): Whisp
-- the guide, the guard's countdown and solved rooms. It defines globals of
-- the room script that requires it, so the rooms call whisp(), go() and
-- the rest directly. The values live in state.player, so every room sees
-- the same ones. Only functions are defined here: a room requires this at
-- its top level, which also runs at world start, with no player.

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
