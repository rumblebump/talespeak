-- Level 2 begins in the lane outside the clock shop. Whoever carries the
-- brass key from Baker Street unlocks the shop; once the safe in the
-- workshop is open, the cab to the river is the save spot.

local case = require("case")

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  local lv = case.lv()
  if lv.safe then
    case.wiggins("The safe is open! The ticket says Wharf 7. Everyone to the cab!")
  elseif not lv.unlocked then
    case.wiggins("Who has the brass key from the cupboard? Unlock the shop!")
  end
  show(player)
end

function show(player)
  local lv = case.lv()
  local list = { { id = "shopdoor", label = lv.unlocked and "open door of the clock shop" or "locked door of the clock shop" } }
  if lv.safe then
    list[#list + 1] = { id = "cab", label = "cab to the river (save spot)" }
  end
  case.items(list)
end

function on_click(player, item)
  if case.click(player, item) then
    return
  end
  act(player, item)
end

function on_input(player, text)
  if case.input(player, text) then
    return
  end
  local t = string.lower(text)
  if case.has(t, "cab") then
    act(player, "cab")
  elseif case.has(t, "door") or case.has(t, "shop") or case.has(t, "unlock") or case.has(t, "key") then
    act(player, "shopdoor")
  else
    say("Try: unlock the door, go into the shop. Type satchel to see what you carry.")
  end
end

function act(player, item)
  if item == "cab" then
    if not case.lv().safe then
      case.wiggins("Not yet! The case is inside the shop.")
      return
    end
    case.ready(player)
    return
  end
  if item ~= "shopdoor" then
    return
  end
  if case.lv().unlocked then
    room.go("shop")
    return
  end
  if not case.carries(player.id, "shop_key") then
    local holder = case.holder("shop_key")
    if holder then
      say("The door is locked. " .. case.name(case.get(), holder) .. " has the brass key.")
    else
      say("The door is locked, and nobody has the key.")
    end
    return
  end
  local ok = case.change(function(lv)
    lv.unlocked = true
    return true
  end)
  if not ok then
    return
  end
  say("The brass key turns. The shop door creaks open, and you step inside.")
  case.news(player, player.name .. " unlocks the clock shop.")
  room.go("shop")
end

function hint(player)
  if not case.lv().unlocked then
    case.wiggins("The one with the brass key clicks the shop door.")
  elseif not case.lv().safe then
    case.wiggins("Go into the shop. The safe in the workshop holds the next clue.")
  else
    case.wiggins("Everyone clicks the cab.")
  end
end

function on_change(player, keys)
  if case.changed(player, keys) then
    return
  end
  show(player)
end

function on_leave(player)
  case.leave(player)
end
