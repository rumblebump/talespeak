-- Level 3 begins at Wharf 7. The harbour map on the notice board tears
-- into one piece per Irregular: only the first piece shows the row
-- letters and only the last one the X, so the players put their pieces
-- together (by talking) to name the crate. The pictures are
-- assets/map-<variant>-<pieces>-<piece>.png. Once the crate is found in
-- the warehouse, the cab to Pall Mall is the save spot.

local case = require("case")

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  if case.lv().crate then
    case.wiggins("A Diogenes Club card! To Pall Mall, everyone!")
  else
    case.wiggins("Someone left a map on the board. Careful, it is old and torn.")
  end
  show(player)
end

function show(player)
  local lv = case.lv()
  local list = {
    { id = "board", label = "notice board with a map" },
    { id = "warehouse", label = "door of the warehouse" },
  }
  if lv.crate then
    list[#list + 1] = { id = "cab", label = "cab to Pall Mall (save spot)" }
  end
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
  if case.has(t, "map") or case.has(t, "board") then
    act(player, "board")
  elseif case.has(t, "warehouse") or case.has(t, "door") or case.has(t, "inside") then
    act(player, "warehouse")
  elseif case.has(t, "cab") then
    act(player, "cab")
  else
    say("Try: take the map, go into the warehouse. Type satchel to see what you carry.")
  end
end

function act(player, item)
  if item == "board" then
    board(player)
  elseif item == "warehouse" then
    room.go("warehouse")
  elseif item == "cab" then
    if not case.lv().crate then
      case.wiggins("Not yet! The watch must be somewhere in that warehouse.")
      return
    end
    case.ready(player)
  end
end

function board(player)
  for k = 1, case.MAX do
    if case.carries(player.id, "map_" .. k) then
      say("You look at your piece of the map again.")
      case.read(player, "map_" .. k)
      return
    end
  end
  -- the piece into the satchel first, then marked taken (nothing waits
  -- in between)
  local lv = case.lv()
  local pieces = lv.pieces or case.n()
  local got = 0
  for k = 1, pieces do
    if not (lv.maps or {})[k] then
      got = k
      break
    end
  end
  if got == 0 then
    say("The board is empty: the others have all the pieces of the map.")
    return
  end
  if not case.give(player.id, "map_" .. got) then
    return
  end
  if not case.change(function(v)
        v.pieces = v.pieces or pieces
        v.maps = v.maps or {}
        v.maps[got] = player.id
        return true
      end) then
    return
  end
  if pieces == 1 then
    say("You take the whole map off the board.")
  else
    say("As you pull, the map tears! You keep piece " .. got .. " of " .. pieces .. ".")
    case.wiggins("Only you can see your piece. Describe it to the others!")
  end
  case.read(player, "map_" .. got)
  case.news(player, player.name .. " takes a piece of the map.")
end

function hint(player)
  local lv = case.lv()
  if lv.crate then
    case.wiggins("Everyone clicks the cab.")
  elseif (lv.maps or {})[1] == nil then
    case.wiggins("Each of you takes a piece of the map from the board.")
  else
    case.wiggins("The X is on one piece, the row letters on another. Which row is the X in? Put the letter and the number together, like A1.")
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
