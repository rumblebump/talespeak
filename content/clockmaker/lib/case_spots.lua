-- Save spots: going on without players who left, and the door at the
-- end of a level that every active Irregular must reach. Part of
-- case.lua, which installs it.

local case, main = {}, nil
setmetatable(case, { __index = function(_, k) return main[k] end })

-- Save spots -----------------------------------------------------------

-- drop takes ids out of the party, which frees their seats for players
-- waiting outside; what they carried goes to the lead's satchel, so
-- nothing the story needs is lost. Coming back, they join again like a
-- new player, when a seat is free.
function case.drop(lead, ids)
  for _, id in ipairs(ids) do
    for _, kind in ipairs({ "bag_", "save_" }) do
      local theirs = state.world.get(kind .. id) or {}
      if #theirs > 0 and id ~= lead then
        state.world.update(kind .. lead, function(v)
          v = v or {}
          for _, it in ipairs(theirs) do
            local found = false
            for _, mine in ipairs(v) do
              if mine == it then
                found = true
              end
            end
            if not found then
              v[#v + 1] = it
            end
          end
          return v
        end)
      end
      if state.world.get(kind .. id) ~= nil then
        state.world.set(kind .. id, nil)
      end
    end
  end
  local gone = {}
  for _, id in ipairs(ids) do
    gone[id] = true
  end
  state.world.update("case", function(v)
    local order = {}
    for _, id in ipairs(v.order) do
      if gone[id] then
        v.members[id] = nil
        v.active[id] = nil
      else
        order[#order + 1] = id
      end
    end
    v.order = order
    return v
  end)
end

-- missing is who the save spot waits for and is not in the world.
local function missing(c)
  local lv = case.lv()
  local online = case.online()
  local out = {}
  for _, id in ipairs(case.active_ids(c)) do
    if not (lv.ready or {})[id] and not online[id] then
      out[#out + 1] = id
    end
  end
  return out
end

-- advance finishes the level: every bag is saved, the progress cleared,
-- and the party moves to the next level, made for the active Irregulars.
local function advance(player)
  local c = case.get()
  for _, id in ipairs(c.order) do
    local kept = {}
    for _, it in ipairs(case.bag(id)) do
      if not case.used_up(it) then
        kept[#kept + 1] = it
      end
    end
    state.world.set("bag_" .. id, kept)
    state.world.set("save_" .. id, kept)
  end
  local n = math.max(1, math.min(#case.active_ids(c), case.MAX))
  state.world.set("lv", { n = n })
  state.world.update("case", function(v)
    v.level = v.level + 1
    v.run = v.run + 1
    return v
  end)
  c = case.get()
  state.player.set("run", c.run)
  case.news(player, "The case moves on: " .. case.LEVELS[c.level].title .. ".")
  room.go(case.LEVELS[c.level].start)
end

-- ready is the save spot: the player waits there, and when every active
-- Irregular waits, the level is done. The lead may go on without those
-- who left the game. Returns true when the party moved on.
function case.ready(player)
  local c = case.get()
  if not c.active[player.id] then
    say("Only the Irregulars on this case can go on.")
    return false
  end
  local first = case.change(function(lv)
    lv.ready = lv.ready or {}
    local was = lv.ready[player.id]
    lv.ready[player.id] = true
    return not was
  end)
  if first == nil then
    return false
  end
  local waiting = {}
  local lv = case.lv()
  for _, id in ipairs(case.active_ids(c)) do
    if not (lv.ready or {})[id] then
      waiting[#waiting + 1] = id
    end
  end
  if #waiting == 0 then
    advance(player)
    return true
  end
  if first then
    case.news(player, player.name .. " waits at the save spot.")
  end
  say("You wait here. Still to come: " .. case.names(c, waiting) .. ".")
  local away = missing(c)
  if #away > 0 and case.lead_id(c) == player.id then
    local choice = ui.dialog(case.names(c, away) .. " left the game. Go on without them? Their seat is freed, and what they carry comes to you.", {
      "Go on without them",
      "Wait for them",
    }, { from = "Wiggins" })
    if choice ~= 1 then
      return false
    end
    -- things may have changed while the dialog was open
    c = case.get()
    if case.lead_id(c) ~= player.id then
      return false
    end
    local still = {}
    for _, id in ipairs(missing(c)) do
      for _, was in ipairs(away) do
        if id == was then
          still[#still + 1] = id
        end
      end
    end
    if #still > 0 then
      case.drop(player.id, still)
    end
    return case.ready(player)
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
