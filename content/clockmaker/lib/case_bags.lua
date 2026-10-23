-- The satchels: what each player carries, giving an item and showing the
-- bag. Part of case.lua, which installs it.

local case, main = {}, nil
setmetatable(case, { __index = function(_, k) return main[k] end })

-- Satchels -------------------------------------------------------------

-- used_up says whether item is only for the level it was found in: the
-- torn strips and the map pieces stay behind at the save spot.
function case.used_up(item)
  return string.sub(item, 1, 6) == "strip_" or string.sub(item, 1, 4) == "map_"
end

function case.bag(id)
  return state.world.get("bag_" .. id) or {}
end

function case.carries(id, item)
  for _, it in ipairs(case.bag(id)) do
    if it == item then
      return true
    end
  end
  return false
end

-- holder is the active Irregular who carries item, or nil.
function case.holder(item)
  local c = case.get()
  for _, id in ipairs(c.order) do
    if case.carries(id, item) then
      return id
    end
  end
  return nil
end

-- give puts item into player id's satchel; false when the world is full.
function case.give(id, item)
  local ok = state.world.update("bag_" .. id, function(v)
    v = v or {}
    for _, it in ipairs(v) do
      if it == item then
        return v
      end
    end
    v[#v + 1] = item
    return v
  end)
  if not ok then
    say(case.FULL)
    return false
  end
  return true
end

function case.show_bag(player)
  local bag = case.bag(player.id)
  if #bag == 0 then
    say("Your satchel is empty.")
    return
  end
  local labels = {}
  for _, it in ipairs(bag) do
    labels[#labels + 1] = case.ITEMS[it] or it
  end
  say("In your satchel: " .. table.concat(labels, ", ") .. ".")
  for _, it in ipairs(bag) do
    case.read(player, it)
  end
end

-- The module returns the function that adds these functions to the case
-- module; inside them, case.x finds the rest of the case there.
return function(m)
  main = m
  for k, f in pairs(case) do
    m[k] = f
  end
end
