-- The casebook (status, hints) and the hooks every room shares: items,
-- clicks and typed input. Part of case.lua, which installs it.

local case, main = {}, nil
setmetatable(case, { __index = function(_, k) return main[k] end })

-- The casebook ---------------------------------------------------------

function case.status(player)
  local c = case.get()
  local online = case.online()
  local lead = case.lead_id(c, online)
  local who = {}
  for _, id in ipairs(c.order) do
    local s = case.name(c, id)
    if id == player.id then
      s = s .. " (you)"
    end
    if not online[id] then
      s = s .. " (away)"
    end
    if id == lead then
      s = s .. " (lead)"
    end
    who[#who + 1] = s
  end
  return "Level " .. c.level .. " of 5: " .. case.LEVELS[c.level].title ..
      ". Made for " .. case.n() .. ". Irregulars: " .. table.concat(who, ", ") .. "."
end

-- casebook shows the party and the level, gives a hint, and lets the lead
-- play the level again with only the Irregulars who are here.
function case.casebook(player)
  local c = case.get()
  say(case.status(player))
  local online = case.online()
  local away = {}
  for _, id in ipairs(case.active_ids(c)) do
    if not online[id] then
      away[#away + 1] = id
    end
  end
  local options = { "Ask Wiggins for a hint", "Close the casebook" }
  local lead = case.lead_id(c, online) == player.id
  if lead and #away > 0 and not c.done then
    options[3] = "Play this level again without " .. case.names(c, away)
  end
  local choice = ui.dialog("Wiggins's casebook", options, { from = "Wiggins" })
  if choice == 1 then
    if hint then
      hint(player)
    else
      case.wiggins("Look around and click what you see.")
    end
  elseif choice == 3 then
    c = case.get()
    online = case.online()
    if case.lead_id(c, online) ~= player.id or c.done then
      return
    end
    away = {}
    for _, id in ipairs(case.active_ids(c)) do
      if not online[id] then
        away[#away + 1] = id
      end
    end
    if #away == 0 then
      return
    end
    case.drop(player.id, away)
    local n = math.max(1, math.min(#case.active_ids(case.get()), case.MAX))
    if case.get().level == 1 then
      n = false -- the telegram asks again
    end
    case.rewind(n)
    case.news(player, "The level starts again, for the Irregulars who are here.")
    case.wiggins("The level starts again, for the Irregulars who are here.")
    state.player.set("run", case.get().run)
    room.go(case.LEVELS[case.get().level].start)
  end
end

-- Hooks the rooms share ------------------------------------------------

-- items shows list plus the satchel and the casebook.
function case.items(list)
  local out = {}
  for _, it in ipairs(list) do
    out[#out + 1] = it
  end
  out[#out + 1] = { id = "satchel", label = "your satchel" }
  out[#out + 1] = { id = "casebook", label = "Wiggins's casebook" }
  ui.items(out)
end

-- click handles the satchel and the casebook; true when it did.
function case.click(player, item)
  if item == "satchel" then
    case.show_bag(player)
    return true
  elseif item == "casebook" then
    case.casebook(player)
    return true
  end
  return false
end

-- input handles what every room understands: satchel, casebook, who.
-- True when it did. Players talk outside the game (a call, or the same
-- table): each sees only their own clues, and the puzzles need them to
-- tell each other.
function case.input(player, text)
  local t = string.lower(text)
  if t == "satchel" or t == "bag" or t == "inventory" or t == "items" or t == "i" then
    case.show_bag(player)
    return true
  end
  if t == "casebook" or t == "hint" or t == "who" or t == "party" then
    case.casebook(player)
    return true
  end
  return false
end

-- The module returns the function that adds these functions to the case
-- module; inside them, case.x finds the rest of the case there.
return function(m)
  main = m
  for k, f in pairs(case) do
    m[k] = f
  end
end
