-- Level 2, the workshop behind the clock shop. With two or more
-- Irregulars, its spring lock shuts behind the first one in, until the
-- safe is open. The note names the clocks whose times open the safe (see
-- shop.lua); the safe gives the lantern and the ticket to the river.

local case = require("case")

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  local n = case.n()
  local lv = case.lv()
  if n > 1 and not lv.safe then
    local shut = case.change(function(v)
      if not v.shut then
        v.shut = player.id
        return "now"
      end
      return v.shut == player.id and "mine" or "other"
    end)
    if shut == "now" then
      say("SLAM! The door swings shut behind you, and a spring lock clicks. You are locked in!")
      case.wiggins("Only the safe will open the door. Read the note, and ask the others about the clocks out there.")
      case.news(player, "SLAM! The workshop door locks behind " .. player.name .. ".")
    end
  end
  show(player)
end

function show(player)
  local lv = case.lv()
  case.items({
    { id = "note", label = "note above the workbench" },
    { id = "safe", label = lv.safe and "open safe" or "safe with clock hands" },
    { id = "door", label = (lv.shut == player.id and not lv.safe) and "locked door" or "door to the shop" },
  })
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
  if case.has(t, "note") then
    act(player, "note")
  elseif case.has(t, "safe") or case.has(t, "hands") then
    act(player, "safe")
  elseif case.has(t, "door") or case.has(t, "shop") or case.has(t, "out") then
    act(player, "door")
  else
    say("Try: read the note, open the safe. Type satchel to see what you carry.")
  end
end

-- clocks says the clocks the safe wants, as the note names them.
function clocks()
  local names = {}
  for i, c in ipairs(case.safe_clocks(case.n())) do
    names[i] = "the " .. c
  end
  return names
end

function act(player, item)
  local lv = case.lv()
  if item == "note" then
    local names = clocks()
    local which
    if #names == 1 then
      which = "the clock with " .. names[1]
    else
      which = "the clocks with " .. table.concat(names, ", then ")
    end
    say("The note, in shaky writing: \"My safe opens when its hands show the time of " .. which .. ". Trust no other clock. A.\"")
  elseif item == "safe" then
    safe(player)
  elseif item == "door" then
    if lv.shut == player.id and not lv.safe then
      say("The door does not move. The spring lock only opens with the safe.")
      return
    end
    room.go("shop")
  end
end

-- time_of turns what the player typed for one time into "h:mm": 7.25,
-- 07:25 and 7:25 are all "7:25".
function time_of(s)
  local out = ""
  for i = 1, #s do
    local ch = string.sub(s, i, i)
    if ch == "." then
      ch = ":"
    end
    out = out .. ch
  end
  while string.sub(out, 1, 1) == "0" and #out > 4 do
    out = string.sub(out, 2)
  end
  return out
end

-- split returns the words of s, split at spaces and commas.
function split(s)
  local out, cur = {}, ""
  for i = 1, #s do
    local ch = string.sub(s, i, i)
    if ch == " " or ch == "," then
      if cur ~= "" then
        out[#out + 1] = cur
      end
      cur = ""
    else
      cur = cur .. ch
    end
  end
  if cur ~= "" then
    out[#out + 1] = cur
  end
  return out
end

function safe(player)
  local lv = case.lv()
  if lv.safe then
    say("The safe is empty now.")
    return
  end
  local want = case.safe_clocks(case.n())
  local times = case.clock_times()
  local example = (#want == 1) and "like 7:25" or "in order, like 7:25 3:40"
  local answer = ui.ask("Set the safe's hands. What time" .. (#want == 1 and "" or "s") .. "? Type " .. example .. ".", { strict = true })
  if not answer then
    return
  end
  local got = split(words.normalize(answer))
  local right = #got == #want
  if right then
    for i, c in ipairs(want) do
      if time_of(got[i]) ~= times[c] then
        right = false
      end
    end
  end
  if not right then
    say("You turn the hands. The safe stays shut.")
    case.wiggins("Read the note again, and ask the others what those clocks show.")
    return
  end
  if case.lv().safe then
    say("Someone opened the safe just before you.")
    return
  end
  -- what the safe holds first, then the open safe (nothing waits in
  -- between, so nobody else takes it meanwhile)
  if not case.give(player.id, "lantern") or not case.give(player.id, "ticket") then
    return
  end
  if not case.change(function(v)
        v.safe = true
        v.shut = nil
        return true
      end) then
    return
  end
  say("Tick, tick, CLUNK. The safe opens, and so does the workshop door! Inside: the Queen's watch case, empty, a storm lantern and a ticket. You take the lantern and the ticket.")
  case.read(player, "ticket")
  case.news(player, player.name .. " opened the safe! The watch is gone, but there is a ticket to Wharf 7. The workshop door is open.")
  show(player)
end

function hint(player)
  local lv = case.lv()
  if lv.safe then
    case.wiggins("Out to the lane and into the cab!")
  else
    case.wiggins("Read the note. Ask the others for the times of those clocks, and type them in the same order.")
  end
end

function on_change(player, keys)
  if case.changed(player, keys) then
    return
  end
  show(player)
end

function on_leave(player)
  if case.lv().shut == player.id then
    -- nobody stays locked out of the workshop while the player is gone
    state.world.update("lv", function(v)
      v = v or {}
      if v.shut == player.id then
        v.shut = nil
      end
      return v
    end)
  end
  case.leave(player)
end
