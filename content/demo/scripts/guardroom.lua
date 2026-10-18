-- The guard room. The key box opens with the code from under the stone in
-- the corridor; the key in it opens the back door, which is the way out.
-- The player has to remember the code: what the player read is the link
-- between the rooms. With the key, the corridor is solved and locked:
-- there is nothing left to do there.
--
-- The guard comes back here too, when the cell's countdown runs out.

CODE = "417"

key = false        -- the player took the key from the box
wrong = 0          -- wrong codes tried
ended = false      -- the story is over
entered = false    -- the player was here before

function on_enter(player)
  room.describe()
  if entered then
    show_items()
    return
  end
  entered = true
  say("Nobody is here. A half-eaten sausage lies next to the cards.")
end

function on_click(player, item)
  if not act() then
    return
  end
  if item == "box" then
    open_box()
  elseif item == "cards" then
    cards()
  elseif item == "backdoor" then
    back_door()
  elseif item == "corridor" then
    to_corridor()
  end
end

-- Typed words, checked from the most to the least specific: "unlock the
-- back door with the key from the box" is the back door.
function on_input(player, text)
  if not act() then
    return
  end
  local t = spaced(text)
  if has(t, "corridor") then
    to_corridor()
  elseif has(t, "back door") or has(t, "backdoor") or has(t, "keyhole") or has(t, "escape")
      or word(t, "leave") or word(t, "out") then
    back_door()
  elseif has(t, "box") or has(t, "wheel") or has(t, "code") then
    open_box()
  elseif has(t, "key") then
    back_door()
  elseif has(t, "card") or has(t, "table") or has(t, "sausage") then
    cards()
  elseif has(t, "door") and key then
    back_door()
  elseif has(t, "door") then
    say("Which door: the narrow back door, or the door to the corridor?")
  elseif word(t, "back") then
    to_corridor()
  elseif #digits(t) >= 3 then
    say("Numbers for the box? Click the box or type \"open the box\" to turn its wheels.")
  elseif has(t, "help") then
    say("Try: open the box, look at the cards, open the back door, go back to the corridor.")
  else
    say("You think about \"" .. text .. "\", but there is no time for that.")
  end
end

function has(text, part)
  return string.find(text, part, 1, true) ~= nil
end

-- spaced turns text into lowercase words between single spaces, with a
-- space at both ends, so word() matches whole words only ("x" but not
-- "box", "out" but not "about").
function spaced(text)
  local out = { " " }
  local t = string.lower(text)
  for i = 1, #t do
    local c = string.sub(t, i, i)
    if (c >= "a" and c <= "z") or (c >= "0" and c <= "9") then
      out[#out + 1] = c
    elseif out[#out] ~= " " then
      out[#out + 1] = " "
    end
  end
  if out[#out] ~= " " then
    out[#out + 1] = " "
  end
  return table.concat(out)
end

function word(t, w)
  return string.find(t, " " .. w .. " ", 1, true) ~= nil
end

function to_corridor()
  if go("corridor") then
    say("You step back into the corridor.")
  end
end

-- act runs before every action: it counts the guard's time away and
-- returns false when the action does not happen.
function act()
  if ended then
    say("The story has ended. Reload the page to play again.")
    return false
  end
  local away = guard_away() or 0
  if away == 0 then
    say("The door bangs open. The guard stands there with his full bottle and stares at you.")
    caught()
    return false
  end
  away = away - 1
  set_guard_away(away)
  if away == 1 then
    say("Out in the corridor, someone is whistling. The guard is back up the stairs!")
  end
  return true
end

function show_items()
  local items = {
    { id = "box", label = key and "open iron box" or "iron box with number wheels" },
    { id = "cards", label = "game of cards" },
    { id = "backdoor", label = "narrow back door" },
  }
  if not key then
    items[#items + 1] = { id = "corridor", label = "door to the corridor" }
  end
  ui.items(items)
end

-- digits keeps only the digits of s, so "4 1 7" and "4-1-7" count too.
function digits(s)
  local out = {}
  for i = 1, #s do
    local c = string.sub(s, i, i)
    if c >= "0" and c <= "9" then
      out[#out + 1] = c
    end
  end
  return table.concat(out)
end

function open_box()
  if key then
    say("The box is empty. The big key is in your hand.")
    return
  end
  local answer = ui.ask("Three number wheels. Which numbers do you turn them to?", { strict = true })
  if digits(answer) == CODE then
    if not solve("corridor", "locked", "You have everything you need from the corridor. The way out is the back door.") then
      return
    end
    key = true
    say("Click, click, click. The box opens. Inside lies a big iron key!")
    show_items()
    return
  end
  wrong = wrong + 1
  say("You turn the wheels. The box stays shut.")
  if wrong >= 2 then
    say("Hint: the map in your cell had an X on it. Did you look at the corridor floor?")
  end
end

function cards()
  say("Someone was winning: a pile of copper coins sits by one chair. A note on the table says: \"Hans owes me three silver.\"")
end

function back_door()
  if not key then
    say("The back door is locked. The keyhole is big, made for a big key.")
    return
  end
  say("The big key turns with a screech. Cool night air rushes in.")
  say("You run across the yard, over the low wall and into the forest.")
  say("You escaped. Well done!")
  finish()
end

function caught()
  say("SLAP! Everything goes black.")
  say("When you wake up, three other guards stand over you. They drag you down endless stairs and throw you into a deep well.")
  say("The walls are smooth and wet. There is no way up. After a few days, you die.")
  finish()
end

function finish()
  ended = true
  ui.items({})
  say("THE END. Reload the page to play again.")
end

-- Shared by the demo's rooms: Whisp the guide, the guard's countdown and
-- solved rooms. The values live in state.player, so every room sees the
-- same ones. Each room script has a copy of this block until packs can
-- share a Lua module (require, T-0026).

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
