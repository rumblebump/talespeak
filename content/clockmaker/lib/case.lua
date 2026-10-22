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
--   case        { level, run, done, members = {id = {name, gm}}, order,
--                 active = {id = true} }; order is who joined first
--   lv          the progress of the current level; empty again at each
--               save spot. lv.n is how many Irregulars the level is made
--               for, lv.ready who waits at the save spot
--   bag_<id>    what player id carries, a list of item ids
--   save_<id>   their bag as it was when the level began
--   online      { id = true } for the players in the world now
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
-- with fewer; both free those players' seats): a GM of the world once the engine tells scripts
-- (player.gm), until then the first Irregular who joined, or the earliest
-- one online.
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

function case.online()
  return state.world.get("online") or {}
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
    if online[id] and c.members[id].gm then
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

-- The world's secrets -------------------------------------------------

-- The word that opens Holmes's desk, torn into one strip per Irregular.
local WORDS = { "PENDULUM", "CLOCKWORK", "HOURGLASS", "TIMEPIECE", "MAINSPRING" }

function case.word()
  return case.secret("word", function()
    return WORDS[math.random(1, #WORDS)]
  end)
end

-- strip is the text on strip k of n: the word cut into n parts, as even
-- as they come.
function case.strip(k, n)
  local w = case.word()
  local size = math.floor(#w / n)
  local extra = #w - size * n
  local from = 1
  for i = 1, k - 1 do
    from = from + size + ((i <= extra) and 1 or 0)
  end
  local len = size + ((k <= extra) and 1 or 0)
  return string.sub(w, from, from + len - 1)
end

-- The clocks in the shop window, each with an animal on its face, and
-- the time each shows.
case.CLOCKS = { "owl", "ship", "rose", "moon", "crown" }

function case.clock_times()
  return case.secret("clocks", function()
    local t = {}
    for _, c in ipairs(case.CLOCKS) do
      t[c] = math.random(1, 12) .. ":" .. string.format("%02d", 5 * math.random(0, 11))
    end
    return t
  end)
end

-- safe_clocks is which clocks the safe wants, in order: one fewer than
-- the Irregulars (at least one, at most three), so everyone outside reads
-- one clock.
function case.safe_clocks(n)
  local order = case.secret("safe_order", function()
    return case.shuffled(case.CLOCKS)
  end)
  local k = math.max(1, math.min(n - 1, 3))
  local out = {}
  for i = 1, k do
    out[i] = order[i]
  end
  return out
end

-- The torn map of the warehouse: the variant (assets/map-<v>-<n>-<k>.png,
-- drawn by scripts/clockmaker-maps.py) and the crate its X marks.
local MAPS = { a = "K8", b = "H3", c = "S4" }

function case.map_variant()
  return case.secret("map", function()
    local v = { "a", "b", "c" }
    return v[math.random(1, 3)]
  end)
end

function case.crate()
  return MAPS[case.map_variant()]
end

-- The suspects; one of them is the thief, and the evidence cards clear
-- the three others.
case.SUSPECTS = {
  { name = "Tobias Finch, the apprentice", alibi = "Tobias Finch was at night school until ten. His teacher signed his book." },
  { name = "Silas Crane, the jeweller", alibi = "Silas Crane was in Brighton all week. The hotel porter swears to it." },
  { name = "Mrs Agatha Pell, the customer", alibi = "Mrs Pell was at the opera that night. Her ticket is stamped." },
  { name = "Mr Dunn, the night watchman", alibi = "Mr Dunn rang the street bell every hour. Everyone heard it." },
}

function case.thief()
  return case.secret("thief", function()
    return math.random(1, #case.SUSPECTS)
  end)
end

-- cards is the text of the four evidence cards: three alibis and one
-- that clears nobody, in a random order.
function case.cards()
  local order = case.secret("cards", function()
    local list = {}
    for i = 1, #case.SUSPECTS do
      list[#list + 1] = i
    end
    return case.shuffled(list)
  end)
  local thief = case.thief()
  local out = {}
  for _, i in ipairs(order) do
    if i == thief then
      out[#out + 1] = "The thief knew the secret of the safe: the times on the clocks."
    else
      out[#out + 1] = case.SUSPECTS[i].alibi
    end
  end
  return out
end

-- read says what an item in the satchel shows.
function case.read(player, item)
  local k = tonumber(string.sub(item, -1))
  if string.sub(item, 1, 6) == "strip_" then
    local n = case.lv().strips or 1
    say("Strip " .. k .. " of " .. n .. " reads: " .. case.strip(k, n))
  elseif string.sub(item, 1, 4) == "map_" then
    local n = case.lv().pieces or 1
    ui.image("map-" .. case.map_variant() .. "-" .. n .. "-" .. k .. ".png",
      "Piece " .. k .. " of " .. n .. " of the torn map of Wharf 7.")
  elseif string.sub(item, 1, 9) == "evidence_" then
    say("Evidence card " .. k .. ": " .. case.cards()[k])
  elseif item == "ticket" then
    say("The ticket says: WHARF 7, WAREHOUSE. COLLECT BY NIGHT.")
  elseif item == "club_card" then
    say("The card says: DIOGENES CLUB, PALL MALL. NO TALKING.")
  end
end

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
    say(FULL)
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

-- Arriving and leaving -------------------------------------------------

-- arrive runs first in every room's on_enter. It makes the player an
-- Irregular (up to four), marks them online and moves them to where the
-- party is. Returns false when the player moves on, then the room does
-- nothing more.
function case.arrive(player)
  local here = player.room
  state.world.update("case", function(v)
    v = v or new_case()
    local m = v.members[player.id]
    if m then
      m.name = player.name
      m.gm = player.gm == true
      v.active[player.id] = true
    elseif #v.order < case.MAX then
      v.members[player.id] = { name = player.name, gm = player.gm == true }
      v.order[#v.order + 1] = player.id
      v.active[player.id] = true
    end
    return v
  end)
  local c = case.get()
  if c.members[player.id] then
    state.world.update("online", function(v)
      v = v or {}
      v[player.id] = true
      return v
    end)
  end
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

-- leave runs in every room's on_leave: the player is no longer online,
-- no longer waits at the save spot, and when they were the last one, the
-- level starts again from its save spot.
function case.leave(player)
  if not case.get().members[player.id] then
    return
  end
  local last = false
  state.world.update("online", function(v)
    v = v or {}
    v[player.id] = nil
    last = next(v) == nil
    return v
  end)
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
  state.world.update("online", function(v)
    v = v or {}
    for id in pairs(gone) do
      v[id] = nil
    end
    return v
  end)
end

-- here_ids is the members in the player's room now.
local function here_ids()
  local out = {}
  for _, p in ipairs(room.players()) do
    out[p.id] = true
  end
  return out
end

-- decider is who decides at a save spot: the lead when they are in this
-- room, else the first Irregular here. (A player who was in the world
-- when the server restarted can stay in "online" for good, so the room
-- decides here, not "online".)
local function decider(c)
  local here = here_ids()
  local lead = case.lead_id(c)
  if lead and here[lead] then
    return lead
  end
  for _, id in ipairs(c.order) do
    if here[id] then
      return id
    end
  end
  return nil
end

-- missing is who the save spot waits for and is not in this room.
local function missing(c)
  local lv = case.lv()
  local here = here_ids()
  local out = {}
  for _, id in ipairs(case.active_ids(c)) do
    if not (lv.ready or {})[id] and not here[id] then
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
-- Irregular waits, the level is done. The lead (or, without them, the
-- first Irregular here) may go on without those who are not in this
-- room. Returns true when the party moved on.
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
  if #away > 0 and decider(c) == player.id then
    local choice = ui.dialog(case.names(c, away) .. " is not here. Go on without them? Their seat is freed, and what they carry comes to you.", {
      "Go on without them",
      "Wait for them",
    }, { from = "Wiggins" })
    if choice ~= 1 then
      return false
    end
    -- things may have changed while the dialog was open
    c = case.get()
    if decider(c) ~= player.id then
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

return case
