-- Room items that players share, split or use up, and effects that wear
-- off (T-0020), for any pack to copy.
--
--   local items = require("items")
--   items.define({
--     { id = "purse", rule = "split", amount = 12, gives = "silver" },
--     { id = "apples", rule = "each", gives = "apple" },
--     { id = "horn", rule = "once" },
--     { id = "torches", rule = "count", count = 3, gives = "torch" },
--   })
--
-- Rules, decided per item:
--   each   every player takes one, once
--   once   the first player to take it has it; gone for everyone else
--   count  first come, first served until count are gone (one player
--          may take several)
--   split  the first take shares amount among the players in the room
--          then (every connection counts); each of them takes their
--          share, and the rest stays
--
-- What players carry is state.player "inv": { silver = 3, apple = 1 }.
-- Shared items live in state.world under "item_<id>", so item ids of
-- shared items must be unique in the pack and at most 59 bytes. A take
-- changes that key, and the other players' rooms hear of it in
-- on_change, where they call items.visible again.
--
-- Effects, such as a blessing, wear off after a time (a player timer
-- "fx_<id>"), after a number of moves, or both. The room calls
-- items.move once per action, items.on_timer in on_timer, and may define
-- on_effect_end(player, id), which runs when one wears off.
--
-- Only functions and locals here: a module also runs at world start, for
-- no player.

local items = {}

-- defs are this room's items by id, from items.define.
local defs = {}

local RULES = { each = true, once = true, count = true, split = true }

local function wkey(id)
  return "item_" .. id
end

-- items.define(list) declares the room's shared items; call it at the
-- top of the room script.
function items.define(list)
  for _, d in ipairs(list) do
    if type(d.id) ~= "string" or #wkey(d.id) > 64 then
      error("items.define: ids are strings of at most 59 bytes", 2)
    end
    if not RULES[d.rule] then
      error("items.define: " .. d.id .. ": rule must be each, once, count or split", 2)
    end
    if d.rule == "count" and (type(d.count) ~= "number" or d.count < 1) then
      error("items.define: " .. d.id .. ": count needs a count", 2)
    end
    if d.rule == "split" and (type(d.amount) ~= "number" or d.amount < 1) then
      error("items.define: " .. d.id .. ": split needs an amount", 2)
    end
    defs[d.id] = d
  end
end

-- items.count(player, name) is how many of name the player carries.
function items.count(player, name)
  local inv = state.player.get("inv") or {}
  return inv[name] or 0
end

-- items.add(player, name, n) gives the player n of name (n may be
-- negative; none left removes it). Returns true, or nil when the game
-- cannot keep it.
function items.add(player, name, n)
  local inv = state.player.get("inv") or {}
  local left = (inv[name] or 0) + n
  if left > 0 then
    inv[name] = left
  else
    inv[name] = nil
  end
  return state.player.set("inv", inv)
end

-- items.inventory(player) lists what the player carries: { {name, n}, ...}
-- sorted by name.
function items.inventory(player)
  local inv = state.player.get("inv") or {}
  local list = {}
  for name, n in pairs(inv) do
    list[#list + 1] = { name = name, n = n }
  end
  table.sort(list, function(a, b)
    return a.name < b.name
  end)
  return list
end

-- share splits d.amount among the players in the room now: the same
-- share each, the rest stays. With fewer than one each, the first to come
-- in get one. present keeps who was there, to tell "none" from "late".
local function share(d)
  local list = room.players()
  local each = math.floor(d.amount / math.max(#list, 1))
  local shares, present, rest = {}, {}, d.amount
  for i, p in ipairs(list) do
    present[p.id] = true
    if each > 0 then
      shares[p.id] = each
      rest = rest - each
    elseif i <= d.amount then
      shares[p.id] = 1
      rest = rest - 1
    end
  end
  return { shares = shares, present = present, rest = rest }
end

-- items.take(player, id) takes item id by its rule. It returns what the
-- player got, { name = "silver", n = 3 }, or nil and why: "unknown",
-- "had" (each, split: the player has theirs), "gone" (once, count; with who
-- took a once item), "late" (split: shared before the player came),
-- "none" (split: the player was there, but there was not enough for
-- everyone) or "busy" (the game cannot keep it right now; nothing is
-- taken then). The room says it in its own words.
function items.take(player, id)
  local d = defs[id]
  if not d then
    return nil, "unknown"
  end
  local name = d.gives or d.id
  if d.rule == "each" then
    local k = "took_" .. id
    if state.player.get(k) then
      return nil, "had"
    end
    if not items.add(player, name, 1) then
      return nil, "busy"
    end
    if not state.player.set(k, true) then
      items.add(player, name, -1)
      return nil, "busy"
    end
    return { name = name, n = 1 }
  end
  local got, why, who = nil, nil, nil
  -- update: no other player takes in between, so a once item has one
  -- owner and a count never goes below zero. The player's bag is filled
  -- inside it: when the game cannot keep that, the item stays where it
  -- was.
  local ok = state.world.update(wkey(id), function(v)
    got, why, who = nil, nil, nil
    local new
    if d.rule == "once" then
      if v then
        why, who = "gone", v.name
        return v
      end
      got, new = 1, { id = player.id, name = player.name }
    elseif d.rule == "count" then
      local left = v and v.left or d.count
      if left <= 0 then
        why = "gone"
        return v
      end
      got, new = 1, { left = left - 1 }
    else
      -- a fresh copy, so v stays as it was if the bag cannot take it
      new = state.world.get(wkey(id)) or share(d)
      got = new.shares[player.id]
      if got == 0 then
        got, why = nil, "had"
        return v
      elseif not got then
        why = new.present and new.present[player.id] and "none" or "late"
        return new -- the first take shares the purse, even with none for this player
      end
      new.shares[player.id] = 0 -- taken
    end
    if not items.add(player, name, got) then
      got, why = nil, "busy"
      return v
    end
    return new
  end)
  if not ok then
    if got then
      items.add(player, name, -got) -- the world did not keep the take
    end
    return nil, "busy"
  end
  if not got then
    return nil, why, who
  end
  return { name = name, n = got }
end

-- items.available(player, id) reports whether the player could take item
-- id now.
function items.available(player, id)
  local d = defs[id]
  if not d then
    return false
  end
  if d.rule == "each" then
    return not state.player.get("took_" .. id)
  end
  local v = state.world.get(wkey(id))
  if v == nil then
    return true
  end
  if d.rule == "once" then
    return false
  elseif d.rule == "count" then
    return v.left > 0
  end
  return (v.shares[player.id] or 0) > 0
end

-- items.visible(player, list) is list (items for ui.items) without the
-- defined items the player cannot take any more.
function items.visible(player, list)
  local out = {}
  for _, it in ipairs(list) do
    if not defs[it.id] or items.available(player, it.id) then
      out[#out + 1] = it
    end
  end
  return out
end

-- items.release(player, id) puts a once item the player took back, for
-- example when they leave the world (on_leave). Returns whether it did.
function items.release(player, id)
  local d = defs[id]
  if not d or d.rule ~= "once" then
    return false
  end
  local v = state.world.get(wkey(id))
  if not v or v.id ~= player.id then
    return false
  end
  return state.world.set(wkey(id), nil) == true
end

-- Effects

local function fx_timer(id)
  return "fx_" .. id
end

-- items.bless(player, id, opts) gives the player the effect id until
-- opts.seconds pass or opts.moves moves are made, whichever comes first
-- (at least one of them). opts.show shows the time left with that label.
-- Starting it again starts it over. Returns true, or nil and why.
function items.bless(player, id, opts)
  if not opts.seconds and not opts.moves then
    error("items.bless: give seconds, moves or both", 2)
  end
  local fx = state.player.get("fx") or {}
  fx[id] = { moves = opts.moves, timed = opts.seconds ~= nil }
  local ok, why = state.player.set("fx", fx)
  if not ok then
    return nil, why
  end
  if opts.seconds then
    return timer.start(fx_timer(id), opts.seconds, { show = opts.show })
  end
  timer.cancel(fx_timer(id))
  return true
end

-- wear_off ends effect id and runs on_effect_end.
local function wear_off(player, id)
  local fx = state.player.get("fx") or {}
  if not fx[id] then
    return
  end
  fx[id] = nil
  state.player.set("fx", fx)
  timer.cancel(fx_timer(id))
  if on_effect_end then
    on_effect_end(player, id)
  end
end

-- items.blessed(player, id) reports whether the effect id is on the
-- player. An effect whose time ran out in a room that does not call
-- items.on_timer wears off here.
function items.blessed(player, id)
  local fx = state.player.get("fx")
  local e = fx and fx[id]
  if not e then
    return false
  end
  if e.timed and not timer.left(fx_timer(id)) then
    wear_off(player, id)
    return false
  end
  return true
end

-- items.move(player) counts one move for every effect on the player that
-- wears off after moves. Call it once per action.
function items.move(player)
  local fx = state.player.get("fx")
  if not fx then
    return
  end
  local over = {}
  for id, e in pairs(fx) do
    if e.moves then
      e.moves = e.moves - 1
      if e.moves <= 0 then
        over[#over + 1] = id
      end
    end
  end
  state.player.set("fx", fx)
  table.sort(over)
  for _, id in ipairs(over) do
    wear_off(player, id)
  end
end

-- items.on_timer(player, name) belongs in on_timer: when an effect's time
-- ran out, it wears off. Returns whether the timer was an effect's.
function items.on_timer(player, name)
  if string.sub(name, 1, 3) ~= "fx_" then
    return false
  end
  wear_off(player, string.sub(name, 4))
  return true
end

return items
