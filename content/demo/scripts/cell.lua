-- The first room of the demo. The player clicks things or types what they
-- do; talking to the guard opens a dialog, the straw a multiple choice, the
-- scratches a picture whose detail depends on a dice roll, and the lock a
-- riddle the player has to spell out.
--
-- Every player runs their own copy of this script: globals are theirs and
-- last until they leave. Lasting progress comes with state.player (T-0018).

local found_nail = false

function on_enter(player)
  room.describe()
  say("A guard leans against the bars, half asleep.")
  say("Click something in the cell, or type what you do.")
end

function on_click(player, item)
  if item == "straw" then
    search()
  elseif item == "guard" then
    talk(player)
  elseif item == "scratches" then
    look_at_wall()
  elseif item == "lock" then
    riddle()
  end
end

-- Typed words: a few keywords are enough for the demo.
function on_input(player, text)
  local t = string.lower(text)
  if has(t, "straw") or has(t, "search") then
    search()
  elseif has(t, "guard") or has(t, "talk") or has(t, "water") then
    talk(player)
  elseif has(t, "wall") or has(t, "scratch") or has(t, "map") then
    look_at_wall()
  elseif has(t, "lock") then
    riddle()
  elseif has(t, "help") then
    say("Try: search the straw, talk to the guard, look at the wall, open the lock.")
  else
    say("You think about \"" .. text .. "\", but nothing comes of it.")
  end
end

function has(text, word)
  return string.find(text, word, 1, true) ~= nil
end

function talk(player)
  local choice = ui.dialog("What do you say to the guard, " .. player.name .. "?", {
    "Ask for water",
    "Ask what time it is",
    "Say nothing",
  })
  if choice == 1 then
    say("The guard grunts and slides a cup of water through the bars.")
  elseif choice == 2 then
    say("\"Late,\" says the guard, and yawns.")
  else
    say("Time passes. Somewhere, a door slams.")
  end
end

function search()
  if found_nail then
    say("Only straw is left.")
    return
  end
  local _, kept = ui.select("Under the straw you find a few things. What do you keep? Pick up to two.", {
    "A bent nail",
    "A smooth pebble",
    "A torn piece of cloth",
  }, { min = 1, max = 2 })
  say("You keep: " .. table.concat(kept, ", ") .. ".")
  for _, label in ipairs(kept) do
    if label == "A bent nail" then
      found_nail = true
      say("The nail might open the lock.")
    end
  end
  ui.items({
    { id = "guard", label = "sleepy guard" },
    { id = "scratches", label = "scratches on the wall" },
    { id = "lock", label = "lock with letter wheels" },
    { id = "straw", label = "flattened straw" },
  })
end

-- The lock opens with a word: typed, spelled right, no typing help.
function riddle()
  say("Four letter wheels. Someone scratched a riddle next to them:")
  local answer = ui.ask("I have bars but no drinks, and you are standing in me. What am I?", { strict = true })
  if words.same(answer, "cell") then
    say("The wheels click into place: C, E, L, L. The lock opens!")
  else
    say("You turn the wheels to \"" .. answer .. "\". Nothing happens.")
  end
end

function look_at_wall()
  local roll = math.random(1, 20)
  if roll >= 10 then
    say("You roll " .. roll .. ": in the dim light the scratches turn into a map.")
    ui.image("wall-map-clear.png", "A map scratched into the wall. An X marks the end of the corridor.")
  else
    say("You roll " .. roll .. ": the scratches are hard to make out.")
    ui.image("wall-map-faint.png", "Faint scratches. Maybe a map?")
  end
end
