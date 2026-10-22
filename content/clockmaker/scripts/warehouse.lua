-- Level 3, the dark warehouse. Only the Irregular with the storm lantern
-- (from the safe) can light it: while they are here, everyone can see.
-- The crates are up in the loft, and the cargo lift only rises with two
-- Irregulars on it (one when the level is for one). Up there, the crate
-- the torn map marks holds the next clue.

local case = require("case")

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  if case.carries(player.id, "lantern") then
    local lit = case.change(function(lv)
      local was = lv.light
      lv.light = player.id
      return not was
    end)
    if lit then
      say("You raise the storm lantern. Now everyone can see!")
      case.news(player, player.name .. " raises the lantern. The warehouse is lit!")
    end
  elseif not case.lv().light then
    say("It is pitch dark. You cannot see your own hands. Who has the lantern?")
  end
  show(player)
end

function lit()
  return case.lv().light ~= nil
end

-- needed is how many Irregulars the lift wants.
function needed()
  return math.min(case.n(), 2)
end

function show(player)
  local lv = case.lv()
  local list = {}
  if lit() then
    local on = (lv.lift or {})[player.id]
    list[#list + 1] = { id = "lift", label = on and "step off the lift" or (lv.loft and "cargo lift (up)" or "cargo lift") }
    if lv.loft then
      list[#list + 1] = { id = "crates", label = lv.crate and "opened crate" or "crates in the loft" }
    end
  else
    list[#list + 1] = { id = "dark", label = "darkness" }
  end
  list[#list + 1] = { id = "wharf", label = "door back to the wharf" }
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
  if case.has(t, "lift") then
    act(player, "lift")
  elseif case.has(t, "crate") or case.has(t, "open") then
    act(player, "crates")
  elseif case.has(t, "wharf") or case.has(t, "out") or case.has(t, "door") then
    act(player, "wharf")
  else
    say("Try: step on the lift, open a crate. Type satchel to see what you carry.")
  end
end

function act(player, item)
  if item == "wharf" then
    put_down(player)
    room.go("wharf")
    return
  end
  if not lit() then
    say("You stumble in the dark and bump your knee. You need light!")
    return
  end
  if item == "lift" then
    lift(player)
  elseif item == "crates" then
    crates(player)
  end
end

-- put_down takes the light (and the player) away when they go.
function put_down(player)
  local lv = case.lv()
  if lv.light == player.id or (lv.lift or {})[player.id] then
    state.world.update("lv", function(v)
      v = v or {}
      if v.light == player.id then
        v.light = nil
      end
      if v.lift then
        v.lift[player.id] = nil
      end
      return v
    end)
  end
end

function lift(player)
  local lv = case.lv()
  if lv.loft then
    say("The lift is up at the loft already.")
    return
  end
  local need = needed()
  local got = case.change(function(v)
    v.lift = v.lift or {}
    if v.lift[player.id] then
      v.lift[player.id] = nil
      return "off"
    end
    v.lift[player.id] = true
    if case.count(v.lift) >= need then
      v.loft = true
      return "up"
    end
    return "on"
  end)
  if got == "off" then
    say("You step off the lift.")
  elseif got == "on" then
    say("You step onto the lift. It creaks, but it does not move. It needs more weight!")
    case.news(player, player.name .. " stands on the cargo lift. It needs more weight!")
  elseif got == "up" then
    say("The chains rattle, and the lift rises to the loft. Crates everywhere!")
    case.news(player, "The chains rattle, and the lift rises to the loft. Crates everywhere!")
  end
  show(player)
end

-- code turns a typed crate name into letter and number: "k 8", "K8" and
-- "8k" are all "K8".
function code(s)
  local letter, number = "", ""
  s = string.upper(s)
  for i = 1, #s do
    local ch = string.sub(s, i, i)
    if ch >= "A" and ch <= "Z" then
      letter = letter .. ch
    elseif ch >= "0" and ch <= "9" then
      number = number .. ch
    end
  end
  return letter .. number
end

function crates(player)
  if not case.lv().loft then
    say("The crates are up in the loft. Take the lift.")
    return
  end
  if case.lv().crate then
    say("The crate is empty now.")
    return
  end
  local answer = ui.ask("Rows and rows of crates, each with a letter and a number. Which one do you open?", { strict = true })
  if not answer then
    return
  end
  if code(answer) == "" then
    say("There is no crate like that. Each crate has a letter and a number.")
    return
  end
  if code(answer) ~= case.crate() then
    say("You pry open crate " .. code(answer) .. ". Only old fishing nets.")
    case.wiggins("The X on the map marks the crate. Find its row letter and its column number.")
    return
  end
  if case.lv().crate then
    say("Crate " .. case.crate() .. " is open already: someone was quicker.")
    return
  end
  -- the card first, then the open crate (nothing waits in between)
  if not case.give(player.id, "club_card") then
    return
  end
  if not case.change(function(v)
        v.crate = true
        return true
      end) then
    return
  end
  say("Inside crate " .. case.crate() .. ": a dozen stolen clocks, but no watch. On top lies a card. You take it.")
  case.read(player, "club_card")
  case.news(player, player.name .. " opened the right crate! Stolen clocks, and a Diogenes Club card. Back to the wharf and the cab!")
  show(player)
end

function hint(player)
  local lv = case.lv()
  if not lit() then
    case.wiggins("The one with the lantern must come in here.")
  elseif not lv.loft then
    case.wiggins("Stand on the lift together: it needs " .. needed() .. ".")
  elseif not lv.crate then
    case.wiggins("Put your map pieces together: the X's row letter and column number name the crate.")
  else
    case.wiggins("Back to the wharf and into the cab!")
  end
end

function on_change(player, keys)
  if case.changed(player, keys) then
    return
  end
  show(player)
end

function on_leave(player)
  put_down(player)
  case.leave(player)
end
