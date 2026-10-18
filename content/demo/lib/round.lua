-- Free and turn-based rounds in a room (T-0019), for any pack to copy.
--
--   local round = require("round")
--
-- A room runs free (everyone acts at once) until a script starts turns:
-- a set order, and only the player whose turn it is may act until they
-- are done. The round lives in state.world under "round_<room id>", so
-- every player of the room sees the same one, and the others hear of
-- each change in on_change.
--
-- Hooks the room script may define (all optional):
--   on_turn(player)                  a turn starts for this player
--   on_round_end(player, n)          round n ended; player ended it
--   on_turn_timeout(player, id, name) the current player (id, name) ran
--                                    out of time; without it, the turn
--                                    is skipped
-- on_turn runs in the instance of the player whose turn starts, often
-- from on_change: it may only say things and set items, not ask or
-- change state.world.
--
-- The room calls round.changed in on_change, round.timeout in on_timer
-- and round.leave in on_leave (and before moving a player out with
-- room.go). Leavers are skipped; players who come in during a round join
-- at the next one.
--
-- Only functions and locals here: a module also runs at world start, for
-- no player.

local round = {}

-- TIMER is the room timer of a turn's deadline.
local TIMER = "round"

-- seen is the turn (serial) this player's instance last started on_turn
-- for, so a turn starts once however often the round changes.
local seen = nil

local function key(player)
  local k = "round_" .. player.room
  if #k > 64 then
    error("round: room ids for rounds are at most 58 bytes", 3)
  end
  return k
end

-- present returns the players in the room by id, and the list.
local function present()
  local ids, list = {}, room.players()
  for _, p in ipairs(list) do
    ids[p.id] = p.name
  end
  return ids, list
end

local function load(player)
  local st = state.world.get(key(player))
  if st and st.mode == "turns" then
    return st
  end
  return nil
end

local function save(player, st)
  return state.world.set(key(player), st)
end

local function call_turn(player, st)
  seen = st.serial
  if on_turn then
    on_turn(player)
  else
    say("Your turn.")
  end
end

-- begin starts the turn of st.order[st.turn]: the deadline for
-- everyone in the room, and on_turn when it is the caller's.
local function begin(player, st)
  local id = st.order[st.turn]
  if st.seconds then
    timer.start(TIMER, st.seconds, { room = true, show = st.names[id] .. "'s turn" })
  else
    timer.cancel(TIMER, { room = true })
  end
  if id == player.id then
    call_turn(player, st)
  end
end

-- advance ends the current turn: the next player in the order who is
-- still in the room acts, or the round ends. gone is a player id to leave
-- out of the next round (one leaving now).
local function advance(player, st, gone)
  local ids, list = present()
  if gone then
    ids[gone] = nil
  end
  local i = st.turn + 1
  while i <= #st.order and not ids[st.order[i]] do
    i = i + 1
  end
  st.serial = st.serial + 1
  if i <= #st.order then
    st.turn = i
    local ok, why = save(player, st)
    if not ok then
      return nil, why
    end
    begin(player, st)
    return true
  end
  -- The round is over. The next one keeps the order of those still here,
  -- then those who came in meanwhile.
  local order, names, inside = {}, {}, {}
  for _, id in ipairs(st.order) do
    if ids[id] then
      order[#order + 1] = id
      inside[id] = true
    end
  end
  for _, p in ipairs(list) do
    if ids[p.id] and not inside[p.id] then
      order[#order + 1] = p.id
    end
  end
  if #order == 0 then
    return round.free(player)
  end
  for _, id in ipairs(order) do
    names[id] = ids[id]
  end
  local ended = st.round
  st.order, st.names, st.turn, st.round, st.skipped = order, names, 1, st.round + 1, nil
  local ok, why = save(player, st)
  if not ok then
    return nil, why
  end
  if on_round_end then
    on_round_end(player, ended)
  end
  -- on_round_end may have stopped the round or started a new order.
  local now = load(player)
  if now and now.serial == st.serial then
    begin(player, now)
  end
  return true
end

-- round.free(player) lets everyone in the player's room act at once.
function round.free(player)
  timer.cancel(TIMER, { room = true })
  return state.world.set(key(player), nil)
end

-- round.turns(player, order, opts) starts turns in the player's room:
-- order is a list of player ids (for example sorted by initiative rolls),
-- or nil for everyone in the room in the order they came in. Ids of
-- players not in the room are left out. opts.seconds gives every turn a
-- deadline. Returns true, or nil and the reason.
function round.turns(player, order, opts)
  local ids, list = present()
  local o, names, seen_ids = {}, {}, {}
  if order == nil then
    order = {}
    for _, p in ipairs(list) do
      order[#order + 1] = p.id
    end
  end
  for _, id in ipairs(order) do
    if ids[id] and not seen_ids[id] then
      o[#o + 1] = id
      names[id] = ids[id]
      seen_ids[id] = true
    end
  end
  if #o == 0 then
    return nil, "nobody here to play"
  end
  local old = state.world.get(key(player))
  local st = {
    mode = "turns",
    order = o,
    names = names,
    turn = 1,
    round = 1,
    seconds = opts and opts.seconds,
    serial = (old and old.serial or 0) + 1,
  }
  local ok, why = save(player, st)
  if not ok then
    return nil, why
  end
  begin(player, st)
  return true
end

-- round.current(player) returns the id and name of the player whose turn
-- it is, or nil when the room runs free.
function round.current(player)
  local st = load(player)
  if not st then
    return nil
  end
  local id = st.order[st.turn]
  return id, st.names[id]
end

-- round.done(player) ends the player's own turn. Returns true, or nil and
-- the reason ("not your turn").
function round.done(player)
  local st = load(player)
  if not st then
    return nil, "no turns here"
  end
  if st.order[st.turn] ~= player.id then
    return nil, "not your turn"
  end
  return advance(player, st)
end

-- round.next(player) ends the current turn, whoever's it is: the script
-- (or later the GM) skips a player.
function round.next(player)
  local st = load(player)
  if not st then
    return nil, "no turns here"
  end
  st.skipped = st.names[st.order[st.turn]]
  return advance(player, st)
end

-- round.leave(player) takes the player out of the round in their room.
-- When it was their turn, the next player's starts.
function round.leave(player)
  local st = load(player)
  if not st then
    return true
  end
  local j = nil
  for i, id in ipairs(st.order) do
    if id == player.id then
      j = i
    end
  end
  if not j then
    return true
  end
  if j == st.turn then
    st.turn = j - 1
    table.remove(st.order, j)
    return advance(player, st, player.id)
  end
  table.remove(st.order, j)
  if j < st.turn then
    st.turn = st.turn - 1
  end
  if #st.order == 0 then
    return round.free(player)
  end
  return save(player, st)
end

-- round.may_act(player) returns true when the player may act now: the
-- room runs free, or it is their turn. Otherwise it tells the player who
-- acts and returns false. A current player who left the room is skipped
-- first.
function round.may_act(player)
  local st = load(player)
  if not st then
    return true
  end
  local id = st.order[st.turn]
  if id == player.id then
    return true
  end
  local ids = present()
  if not ids[id] then
    advance(player, st)
    st = load(player)
    if not st or st.order[st.turn] == player.id then
      return true
    end
    id = st.order[st.turn]
  end
  say("Wait for your turn: " .. st.names[id] .. " is acting.")
  return false
end

-- round.status(player) is a line on the round for the player: who acts,
-- the order, and who was skipped; nil when the room runs free.
function round.status(player)
  local st = load(player)
  if not st then
    return nil
  end
  local names = {}
  for i, id in ipairs(st.order) do
    local n = st.names[id]
    if id == player.id then
      n = n .. " (you)"
    end
    if i == st.turn then
      n = n .. " (acting)"
    end
    names[#names + 1] = n
  end
  local line = "Round " .. st.round .. ". Order: " .. table.concat(names, ", ") .. "."
  if st.skipped then
    line = st.skipped .. " took too long. " .. line
  end
  return line
end

-- round.changed(player, keys) belongs in on_change (and in on_enter, with
-- keys nil): when the round changed and a turn starts for the player, it
-- runs on_turn. Returns whether the round of the room changed.
function round.changed(player, keys)
  local k = key(player)
  local hit = keys == nil
  for _, changed in ipairs(keys or {}) do
    if changed == k then
      hit = true
    end
  end
  if not hit then
    return false
  end
  local st = load(player)
  if st and st.order[st.turn] == player.id and seen ~= st.serial then
    call_turn(player, st)
  end
  return true
end

-- round.timeout(player, name, kind) belongs in on_timer: when the turn's
-- deadline passed, it runs on_turn_timeout or skips the turn. Returns
-- whether the timer was the round's.
function round.timeout(player, name, kind)
  if name ~= TIMER or kind ~= "room" then
    return false
  end
  local st = load(player)
  if not st then
    return true
  end
  local id = st.order[st.turn]
  if on_turn_timeout then
    on_turn_timeout(player, id, st.names[id])
  else
    round.next(player)
  end
  return true
end

return round
