-- Level 2, the clock shop. Five clocks stand along the wall, each showing
-- its own time. The safe in the workshop opens at the times of some of
-- them, but which ones only the note in the workshop says, and the
-- workshop door locks behind the first Irregular who goes in (when the
-- level is for two or more). So the one inside reads the note, the ones
-- outside read the clocks, and they tell each other.

local case = require("case")

CLOCK_LABELS = {
  owl = "clock with the owl",
  ship = "clock with the ship",
  rose = "clock with the rose",
  moon = "clock with the moon",
  crown = "clock with the crown",
}

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  show(player)
end

function show(player)
  local lv = case.lv()
  local list = {}
  for _, c in ipairs(case.CLOCKS) do
    list[#list + 1] = { id = c, label = CLOCK_LABELS[c] }
  end
  local door = "door to the workshop"
  if lv.safe then
    door = "open door to the workshop"
  elseif lv.shut then
    door = "locked door to the workshop"
  end
  list[#list + 1] = { id = "workshop", label = door }
  list[#list + 1] = { id = "lane", label = "door to the lane" }
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
  for _, c in ipairs(case.CLOCKS) do
    if case.has(t, c) then
      return act(player, c)
    end
  end
  if case.has(t, "workshop") then
    act(player, "workshop")
  elseif case.has(t, "lane") or case.has(t, "out") then
    act(player, "lane")
  else
    say("Try: look at the owl clock, go into the workshop. Type satchel to see what you carry.")
  end
end

function act(player, item)
  if CLOCK_LABELS[item] then
    local times = case.clock_times()
    say("The " .. CLOCK_LABELS[item] .. " shows " .. times[item] .. ".")
    if case.carries(player.id, "lens") then
      say("Through Holmes's magnifying glass you see tiny scratches around the winding hole. Someone has set these clocks on purpose.")
    end
  elseif item == "workshop" then
    local lv = case.lv()
    if lv.shut and lv.shut ~= player.id and not lv.safe then
      say("The workshop door is locked. Through the speaking tube you hear " .. case.name(case.get(), lv.shut) .. " inside. Talk to them!")
      return
    end
    room.go("workshop")
  elseif item == "lane" then
    room.go("lane")
  end
end

function hint(player)
  local lv = case.lv()
  if lv.safe then
    case.wiggins("The safe is open. Back to the lane and into the cab!")
  elseif lv.shut then
    case.wiggins("Ask the one in the workshop which clocks the note names, then tell them the times those clocks show.")
  else
    case.wiggins("Look at the clocks, then someone goes into the workshop.")
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
