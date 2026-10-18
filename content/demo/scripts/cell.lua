-- The first room of the demo. The player clicks things or types what they
-- do; talking to the guard opens a dialog, the straw a multiple choice, the
-- scratches a picture whose detail depends on a dice roll, and the lock a
-- riddle the player has to spell out.
--
-- To escape, the player first gets rid of the guard: asking for water three
-- times empties his bottle and he goes to refill it. Trying the door while
-- he watches ends the story in a well; getting out while he is away leads
-- into the corridor (corridor.lua). He is back after AWAY_MOVES actions,
-- counted in every room (a real countdown comes with timers, T-0012).
-- Once out, the cell is solved: going back, Whisp the guide asks first.
--
-- Every player runs their own copy of this script: globals are theirs and
-- last until they leave. What the rooms share is in state.player.

require("rooms") -- lib/rooms.lua: whisp, keep, solve, go and the guard's countdown

AWAY_MOVES = 10

local found_nail = false
local searched = false
wrong = 0      -- wrong answers to the riddle
opened = false -- the lock is open
cups = 0       -- cups of water from the guard's current bottle
ended = false  -- the story is over
left = false   -- the player went out into the corridor

function on_enter(player)
  room.describe()
  if left then
    show_items()
    say("Your old cell. Outside, the guard's stool is still empty.")
    return
  end
  say("A guard leans against the bars, half asleep.")
  say("Click something in the cell, or type what you do.")
end

function on_click(player, item)
  if not act() then
    return
  end
  if item == "straw" then
    search()
  elseif item == "guard" then
    talk(player)
  elseif item == "scratches" then
    look_at_wall()
  elseif item == "lock" then
    try_door()
  end
end

-- Typed words: a few keywords are enough for the demo.
function on_input(player, text)
  if not act() then
    return
  end
  local t = string.lower(text)
  if has(t, "straw") or has(t, "search") then
    search()
  elseif has(t, "guard") or has(t, "talk") or has(t, "water") then
    talk(player)
  elseif has(t, "wall") or has(t, "scratch") or has(t, "map") then
    look_at_wall()
  elseif has(t, "lock") or has(t, "door") or has(t, "escape") or has(t, "leave") or has(t, "corridor") or t == "out" or has(t, "go out") then
    try_door()
  elseif has(t, "help") then
    say("Try: search the straw, talk to the guard, look at the wall, open the lock.")
  else
    say("You think about \"" .. text .. "\", but nothing comes of it.")
  end
end

function has(text, word)
  return string.find(text, word, 1, true) ~= nil
end

-- act runs before every action. It counts the guard's time away, brings
-- him back, and returns false when the action does not happen.
function act()
  if ended then
    say("The story has ended. Reload the page to play again.")
    return false
  end
  local away = guard_away()
  if away == nil then
    return true
  end
  if away == 0 then
    set_guard_away(nil)
    say("Footsteps. The guard is back with a full bottle.")
    if opened then
      say("He sees the open lock.")
      caught()
      return false
    end
    show_items()
    return true
  end
  away = away - 1
  set_guard_away(away)
  if away == 1 then
    say("Far down the corridor, footsteps are coming closer.")
  end
  return true
end

function talk(player)
  if guard_away() then
    say("The guard's stool is empty. Far away, water splashes into a bottle.")
    return
  end
  local choice = ui.dialog("What do you say to the guard, " .. player.name .. "?", {
    "Ask for water",
    "Ask what time it is",
    "Say nothing",
  })
  if choice == 1 then
    cups = cups + 1
    if cups == 1 then
      say("The guard grunts and slides a cup of water through the bars.")
    elseif cups == 2 then
      say("\"Again?\" The guard fills another cup from his bottle and pushes it through the bars.")
    else
      if not set_guard_away(AWAY_MOVES) then
        return
      end
      say("The guard shakes his bottle. Empty! He grumbles and stomps off down the corridor to refill it.")
      say("Now is your chance, but he will not be gone long.")
      cups = 0
      show_items()
    end
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
  searched = true
  say("You keep: " .. table.concat(kept, ", ") .. ".")
  for _, label in ipairs(kept) do
    if label == "A bent nail" then
      found_nail = true
      say("The nail might open the lock.")
    end
  end
  show_items()
end

-- show_items updates the clickable things to what the player changed.
function show_items()
  ui.items({
    { id = "straw", label = searched and "flattened straw" or "heap of straw" },
    { id = "guard", label = guard_away() and "empty stool" or "sleepy guard" },
    { id = "scratches", label = "scratches on the wall" },
    { id = "lock", label = opened and "open cell door" or "lock with letter wheels" },
  })
end

-- try_door is every go at the lock or the door. While the guard watches
-- it is a bad idea; while he is away it is the riddle, and once the lock
-- is open, the way out.
function try_door()
  if not guard_away() then
    local choice = ui.dialog("The guard is watching you. Try the door anyway?", {
      "Yes, quickly",
      "Better not",
    })
    if choice == 1 then
      caught()
    else
      say("You step back from the door. The guard yawns.")
    end
    return
  end
  if opened then
    escaped()
  else
    riddle()
  end
end

-- The lock opens with a word: typed, spelled right, no typing help. A
-- wrong answer gets a hint, a little stronger each time.
function riddle()
  say("Four letter wheels. Someone scratched a riddle next to them:")
  local answer = ui.ask("I have bars but no drinks, and you are standing in me. What am I?", { strict = true })
  if words.same(answer, "cell") then
    say("The wheels click into place: C, E, L, L. The lock opens!")
    opened = true
    show_items()
    return
  end
  wrong = wrong + 1
  local word = words.normalize(answer)
  if #word ~= 4 or string.find(word, " ", 1, true) then
    say("The lock has four letter wheels: the answer is one word of four letters.")
  else
    say("You turn the wheels to " .. string.upper(word) .. ". Nothing happens.")
  end
  if wrong >= 2 then
    say("Hint: look around you. What kind of room has bars?")
  end
  say("Click the lock or type \"open the lock\" to try again.")
end

function caught()
  say("SLAP! The guard's hand shoots through the bars. Everything goes black.")
  say("When you wake up, three other guards stand over you. They drag you down endless stairs and throw you into a deep well.")
  say("The walls are smooth and wet. There is no way up. After a few days, you die.")
  finish()
end

function escaped()
  if not solve("cell", "ask", "Do you really want to get locked in again?") then
    return
  end
  say("You push the cell door open and tiptoe out, past the empty stool.")
  left = true
  room.go("corridor")
end

function finish()
  ended = true
  ui.items({})
  say("THE END. Reload the page to play again.")
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
