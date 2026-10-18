-- The corridor outside the cell. The player is here only while the guard
-- is away refilling his bottle, and he comes back after GUARD_MOVES
-- actions. Under the stone with the X (from the wall map in the cell) is
-- the code for the key box in the guard room; the stairs lead straight to
-- the guard.
--
-- Every room counts the guard's time on its own: one shared countdown
-- comes with state.player (T-0018) or timers (T-0012).

GUARD_MOVES = 5

away = GUARD_MOVES -- actions left until the guard is back
lifted = false     -- the player lifted the stone and read the note
ended = false      -- the story is over
entered = false    -- the player was here before

function on_enter(player)
  room.describe()
  if entered then
    show_items()
    say("The torch still smokes. Down the stairs, water is still splashing.")
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
    say("You open the heavy door as quietly as you can.")
    room.go("guardroom")
  elseif item == "stairs" then
    stairs()
  elseif item == "cell" then
    say("You slip back into your cell.")
    room.go("cell")
  end
end

function on_input(player, text)
  if not act() then
    return
  end
  local t = string.lower(text)
  if has(t, "stone") or has(t, "floor") or has(t, " x") or t == "x" then
    lift_stone()
  elseif has(t, "door") or has(t, "guards") or has(t, "ahead") then
    say("You open the heavy door as quietly as you can.")
    room.go("guardroom")
  elseif has(t, "stair") or has(t, "down") or has(t, "water") then
    stairs()
  elseif has(t, "cell") or has(t, "back") then
    say("You slip back into your cell.")
    room.go("cell")
  elseif has(t, "help") then
    say("Try: look at the stone, open the door, go down the stairs, go back to the cell.")
  else
    say("You think about \"" .. text .. "\", but the footsteps below make you hurry.")
  end
end

function has(text, word)
  return string.find(text, word, 1, true) ~= nil
end

-- act runs before every action: it counts the guard's time away and
-- returns false when the action does not happen.
function act()
  if ended then
    say("The story has ended. Reload the page to play again.")
    return false
  end
  if away == 0 then
    say("The guard comes up the stairs with a full bottle and sees you standing in the corridor.")
    caught()
    return false
  end
  away = away - 1
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
  if lifted then
    say("Under the stone there is only dust now. The note said: BOX 4 1 7.")
    return
  end
  lifted = true
  say("You lift the stone with the X. Under it lies a scrap of paper:")
  say("BOX 4 1 7")
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
