-- The case: the party of Irregulars, the five levels with their save
-- spots, and the satchels the players carry from level to level. Every
-- room script loads it with
--
--   local case = require("case")
--
-- and wires it into its hooks (see any room in scripts/).
--
-- What lasts, all in state.world so that one player's hook can move the
-- whole party:
--
--   case        { level, run, done, members = {id = {name}}, order,
--                 active = {id = true} }; order is who joined first
--   lv          the progress of the current level; empty again at each
--               save spot. lv.n is how many Irregulars the level is made
--               for, lv.ready who waits at the save spot
--   bag_<id>    what player id carries, a list of item ids
--   save_<id>   their bag as it was when the level began
--   secret      what this world picked: words, times, the thief
--   news        a line for the other players, heard in on_change
--
-- A save spot is a door at the end of a level: once every active
-- Irregular waits there, the level is done, the bags are saved and the
-- party moves on. When everyone has left the world, the level starts
-- again from its save spot: progress is cleared and the bags are as they
-- were there. Coming back, a player finds the party's level.
--
-- The lead decides for the party (go on without someone, play the level
-- with fewer; both free those players' seats): a GM of the world who is
-- online and on the case (player.gm), else the first Irregular online.
-- Who is online is world.players(), which is right after a restart too.
--
-- Only functions and locals at the top level: a module also runs at
-- world start, for no player.

local case = {}

case.MAX = 4

case.LEVELS = {
  { start = "study", title = "221B Baker Street" },
  { start = "lane", title = "The clock shop" },
  { start = "wharf", title = "The Thames at night" },
  { start = "club", title = "The Diogenes Club" },
  { start = "yard", title = "Scotland Yard" },
}

local LEVEL_OF = {
  study = 1,
  lane = 2, shop = 2, workshop = 2,
  wharf = 3, warehouse = 3,
  club = 4,
  yard = 5,
}

case.ITEMS = {
  strip_1 = "torn strip of paper (1)",
  strip_2 = "torn strip of paper (2)",
  strip_3 = "torn strip of paper (3)",
  strip_4 = "torn strip of paper (4)",
  lens = "Holmes's magnifying glass",
  shop_key = "brass key to the clock shop",
  lantern = "storm lantern",
  ticket = "ticket for Wharf 7",
  map_1 = "piece of a torn map (1)",
  map_2 = "piece of a torn map (2)",
  map_3 = "piece of a torn map (3)",
  map_4 = "piece of a torn map (4)",
  club_card = "Diogenes Club card",
  evidence_1 = "evidence card (1)",
  evidence_2 = "evidence card (2)",
  evidence_3 = "evidence card (3)",
  evidence_4 = "evidence card (4)",
}

local FULL = "(The game cannot remember that right now. Try again in a moment.)"
case.FULL = FULL

function case.wiggins(text)
  say(text, { from = "Wiggins" })
end

function case.holmes(text)
  say(text, { from = "Holmes" })
end

-- has says whether text contains word (text already lowercase).
function case.has(text, word)
  return string.find(text, word, 1, true) ~= nil
end

local function count(t)
  local n = 0
  for _ in pairs(t or {}) do
    n = n + 1
  end
  return n
end
case.count = count

local function new_case()
  return { level = 1, run = 1, done = false, members = {}, order = {}, active = {} }
end

function case.get()
  return state.world.get("case") or new_case()
end

function case.level_of(room_id)
  return LEVEL_OF[room_id]
end

-- online is the players in the world now, by id: {id = {name, room, gm}}.
function case.online()
  local out = {}
  for _, p in ipairs(world.players()) do
    out[p.id] = p
  end
  return out
end

function case.name(c, id)
  local m = c.members[id]
  return m and m.name or "someone"
end

function case.names(c, ids)
  local out = {}
  for _, id in ipairs(ids) do
    out[#out + 1] = case.name(c, id)
  end
  if #out == 0 then
    return "nobody"
  end
  return table.concat(out, ", ")
end

-- active_ids is the active Irregulars in the order they joined.
function case.active_ids(c)
  local out = {}
  for _, id in ipairs(c.order) do
    if c.active[id] then
      out[#out + 1] = id
    end
  end
  return out
end

-- lead_id is who decides for the party now: an online GM, else the first
-- Irregular online.
function case.lead_id(c, online)
  online = online or case.online()
  for _, id in ipairs(c.order) do
    if online[id] and online[id].gm then
      return id
    end
  end
  for _, id in ipairs(c.order) do
    if online[id] then
      return id
    end
  end
  return nil
end

function case.is_lead(player)
  return case.lead_id(case.get()) == player.id
end

-- Level progress -------------------------------------------------------

function case.lv()
  return state.world.get("lv") or {}
end

-- change runs fn(lv) on the level's progress with nobody in between, and
-- keeps what fn changed. Returns what fn returned, or nil and says so
-- when the world cannot keep it.
function case.change(fn)
  local result
  local ok = state.world.update("lv", function(v)
    v = v or {}
    result = fn(v)
    return v
  end)
  if not ok then
    say(FULL)
    return nil
  end
  return result
end

-- n is how many Irregulars the level is made for, 1 to 4: fixed when the
-- level starts, or by the first step that hands out parts.
function case.n()
  local n = case.lv().n
  if n then
    return n
  end
  n = #case.active_ids(case.get())
  if n < 1 then
    n = 1
  end
  return math.min(n, case.MAX)
end

-- freeze fixes the level's size now, if it is not fixed yet; returns it.
-- Returns nil when the world cannot keep it.
function case.freeze()
  local n = case.n()
  local ok = case.change(function(lv)
    lv.n = lv.n or n
    return true
  end)
  if not ok then
    return nil
  end
  return case.lv().n
end

-- A secret the world picks once, with make(), and keeps through every
-- save spot and restart.
function case.secret(key, make)
  local s = state.world.get("secret")
  if s and s[key] ~= nil then
    return s[key]
  end
  local new = make()
  state.world.update("secret", function(v)
    v = v or {}
    if v[key] == nil then
      v[key] = new
    end
    return v
  end)
  s = state.world.get("secret")
  if s and s[key] ~= nil then
    return s[key]
  end
  return new
end

-- shuffled returns a copy of list in a random order.
function case.shuffled(list)
  local out = {}
  for i, v in ipairs(list) do
    out[i] = v
  end
  for i = #out, 2, -1 do
    local j = math.random(1, i)
    out[i], out[j] = out[j], out[i]
  end
  return out
end

-- News ----------------------------------------------------------------

-- news tells the other players something; they hear it in on_change.
function case.news(player, text)
  state.world.update("news", function(v)
    return { n = (v and v.n or 0) + 1, text = text, skip = player and player.id }
  end)
end

-- Arriving and leaving -------------------------------------------------

-- arrive runs first in every room's on_enter. It makes the player an
-- Irregular (up to four) and moves them to where the party is. Returns false when the player moves on, then the room does
-- nothing more.
function case.arrive(player)
  local here = player.room
  state.world.update("case", function(v)
    v = v or new_case()
    local m = v.members[player.id]
    if m then
      m.name = player.name
      v.active[player.id] = true
    elseif #v.order < case.MAX then
      v.members[player.id] = { name = player.name }
      v.order[#v.order + 1] = player.id
      v.active[player.id] = true
    end
    return v
  end)
  local c = case.get()
  -- the news so far is old news for this room
  local nws = state.world.get("news")
  news_seen = nws and nws.n or 0
  if not c.members[player.id] then
    if here ~= "pavement" then
      room.go("pavement")
      return false
    end
    return true
  end
  local start = case.LEVELS[c.level].start
  if LEVEL_OF[here] ~= c.level then
    room.go(start)
    return false
  end
  if (state.player.get("run") or 0) ~= c.run then
    if here ~= start then
      room.go(start)
      return false
    end
    state.player.set("run", c.run)
  end
  return true
end

-- leave runs in every room's on_leave: the player no longer waits at the
-- save spot, and when they were the last Irregular in the world, the
-- level starts again from its save spot. (on_leave does not run when the
-- server stops: after a restart, the level goes on where it was.)
function case.leave(player)
  local c = case.get()
  if not c.members[player.id] then
    return
  end
  local last = true
  for _, p in ipairs(world.players()) do
    if c.members[p.id] then
      last = false
    end
  end
  local lv = case.lv()
  if lv.ready and lv.ready[player.id] then
    state.world.update("lv", function(v)
      v = v or {}
      if v.ready then
        v.ready[player.id] = nil
      end
      return v
    end)
  end
  if last then
    case.rewind()
  end
end

-- rewind starts the current level again from its save spot: progress
-- cleared, every bag as it was there, and a new run, so returning players
-- start at the level's first room. The level keeps its size, unless n
-- gives a new one (false: none yet).
function case.rewind(n)
  local c = case.get()
  if c.done then
    return
  end
  local lv = case.lv()
  if n ~= nil then
    state.world.set("lv", { n = n or nil })
  else
    -- the same size: the strips of level 1 stay as many
    state.world.set("lv", { n = lv.n, strips = lv.strips })
  end
  for _, id in ipairs(c.order) do
    state.world.set("bag_" .. id, state.world.get("save_" .. id))
  end
  state.world.update("case", function(v)
    v = v or new_case()
    v.run = v.run + 1
    return v
  end)
end

-- changed runs first in every room's on_change: it says the news and
-- moves the player when the party moved on.
-- Returns true when the player moves, then the room does nothing more.
function case.changed(player, keys)
  local nws = state.world.get("news")
  if nws and nws.n > (news_seen or 0) then
    news_seen = nws.n
    if nws.skip ~= player.id then
      say(nws.text)
    end
  end
  local c = case.get()
  if not c.members[player.id] then
    if player.room ~= "pavement" then
      room.go("pavement") -- the party went on without them
      return true
    end
    return false
  end
  local start = case.LEVELS[c.level].start
  if LEVEL_OF[player.room] ~= c.level then
    room.go(start)
    return true
  end
  if (state.player.get("run") or 0) ~= c.run then
    if player.room ~= start then
      room.go(start)
      return true
    end
    -- the level started again here: show it afresh
    state.player.set("run", c.run)
    room.go(start)
    return true
  end
  return false
end

-- The rest of the case, in modules of their own by function.
for _, name in ipairs({ "case_secrets", "case_bags", "case_spots", "case_input" }) do
  require(name)(case)
end

return case
