-- The corridor outside the cell. The player is here only while the guard
-- is away refilling his bottle; he comes back when the countdown the cell
-- started runs out. Under the stone with the X (from the wall map in the
-- cell) is the code for the key box in the guard room, a new one in every
-- world (box_code in lib/rooms.lua); the stairs lead straight to the
-- guard. Going back into the solved cell, Whisp asks first.

require("rooms") -- whisp, the guard's countdown, solved rooms, the box code: lib/rooms.lua

lifted = false     -- the player lifted the stone and read the note
ended = false      -- the story is over
entered = false    -- the player was here before

function on_enter(player)
  room.describe()
  if entered then
    show_items()
    if (guard_away() or 0) <= 1 then
      say("The torch still smokes. Footsteps are coming up the stairs!")
    else
      say("The torch still smokes. Down the stairs, water is still splashing.")
    end
    return
  end
  entered = true
  say("You are out! Somewhere down the stairs the guard is filling his bottle. He will not be long.")
end

function on_click(player, item)
  if not act() then
    return
  end
  if item == "stone" then
    lift_stone()
  elseif item == "door" then
    to_guardroom()
  elseif item == "stairs" then
    stairs()
  elseif item == "cell" then
    to_cell()
  end
end

-- Typed words, checked from the most to the least specific: "the cell
-- door" is the cell, "back to the guard room" is the guard room.
function on_input(player, text)
  if not act() then
    return
  end
  local t = spaced(text)
  if has(t, "stone") or has(t, "floor") or word(t, "x") then
    lift_stone()
  elseif has(t, "guard") then
    to_guardroom()
  elseif has(t, "cell") then
    to_cell()
  elseif has(t, "stair") or word(t, "down") or has(t, "water") or has(t, "well") then
    stairs()
  elseif has(t, "door") or has(t, "ahead") then
    to_guardroom()
  elseif word(t, "back") then
    to_cell()
  elseif has(t, "help") then
    say("Try: look at the stone, open the door, go down the stairs, go back to the cell.")
  else
    say("You think about \"" .. text .. "\", but the footsteps below make you hurry.")
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

function to_guardroom()
  if go("guardroom") then
    say("You open the heavy door as quietly as you can.")
  end
end

function to_cell()
  if go("cell") then
    say("You slip back into your cell.")
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
    say("The guard comes up the stairs with a full bottle and sees you standing in the corridor.")
    caught()
    return false
  end
  away = away - 1
  set_guard_away(away)
  if away == 1 then
    say("Footsteps on the stairs: the guard is coming back up!")
  end
  return true
end

function show_items()
  ui.items({
    { id = "stone", label = lifted and "lifted stone" or "stone with an X" },
    { id = "door", label = "door marked GUARDS" },
    { id = "stairs", label = "stairs down" },
    { id = "cell", label = "your cell" },
  })
end

function lift_stone()
  local code = box_code()
  if not code then
    return
  end
  if lifted then
    say("Under the stone there is only dust now. The note said: BOX " .. spelled(code) .. ".")
    return
  end
  lifted = true
  say("You lift the stone with the X. Under it lies a scrap of paper:")
  say("BOX " .. spelled(code))
  show_items()
end

function stairs()
  local choice = ui.dialog("The splashing comes from down there. Go down the stairs?", {
    "Yes, go down",
    "No, stay up here",
  })
  if choice == 1 then
    say("At the bottom of the stairs you walk straight into the guard, who is filling his bottle at a well.")
    caught()
  else
    say("You stay in the corridor.")
  end
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
