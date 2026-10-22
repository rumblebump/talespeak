-- Level 4, the Diogenes Club, where nobody speaks. The porter hands each
-- Irregular a card with the number of the hat peg for the NEXT Irregular,
-- never their own: everyone has to find out their peg from someone else.
-- (A challenge for the players: solve it without speaking, only by
-- pointing and holding up fingers.) When every hat hangs on its peg, the
-- porter opens the Strangers' Room, where Mycroft Holmes deals the
-- evidence cards for the last level. The cab is the save spot.

local case = require("case")

PEGS = 8

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  local lv = case.lv()
  if lv.dealt then
    case.wiggins("We have the evidence. To Scotland Yard!")
  elseif not lv.club then
    case.wiggins("Shh! Nobody talks in here. Let's see what the porter wants. Can you do this level without speaking at all?")
  end
  show(player)
end

function show(player)
  local lv = case.lv()
  local list = {
    { id = "porter", label = "porter in a red coat" },
    { id = "pegs", label = "hat pegs 1 to 8" },
  }
  if lv.open then
    list[#list + 1] = { id = "mycroft", label = "gentleman in the Strangers' Room" }
  end
  if lv.dealt then
    list[#list + 1] = { id = "cab", label = "cab to Scotland Yard (save spot)" }
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
  if case.has(t, "porter") or case.has(t, "card") then
    act(player, "porter")
  elseif case.has(t, "peg") or case.has(t, "hat") then
    act(player, "pegs")
  elseif case.has(t, "gentleman") or case.has(t, "mycroft") or case.has(t, "stranger") then
    act(player, "mycroft")
  elseif case.has(t, "cab") then
    act(player, "cab")
  else
    say("A gentleman lowers his newspaper and frowns at you. Try: the porter, the hat pegs.")
  end
end

function act(player, item)
  if item == "porter" then
    porter(player)
  elseif item == "pegs" then
    pegs(player)
  elseif item == "mycroft" then
    mycroft(player)
  elseif item == "cab" then
    if not case.lv().dealt then
      case.wiggins("Not yet! First the porter's riddle.")
      return
    end
    case.ready(player)
  end
end

-- deal picks a peg for every active Irregular, once.
function deal()
  local ids = case.active_ids(case.get())
  local pegs = {}
  for i = 1, PEGS do
    pegs[i] = i
  end
  pegs = case.shuffled(pegs)
  case.change(function(lv)
    if lv.club then
      return
    end
    lv.club = {}
    lv.pegs = {}
    for i, id in ipairs(ids) do
      lv.club[i] = id
      lv.pegs[id] = pegs[i]
    end
  end)
  return case.lv()
end

function porter(player)
  local lv = deal()
  if not lv.club then
    return
  end
  local c = case.get()
  local mine = 0
  for i, id in ipairs(lv.club) do
    if id == player.id then
      mine = i
    end
  end
  if mine == 0 then
    say("The porter looks at you and shakes his head. He has no card for you. Help the others!")
    return
  end
  local nxt = lv.club[mine % #lv.club + 1]
  if nxt == player.id then
    say("The porter silently hands you a card: YOUR HAT GOES ON PEG " .. lv.pegs[nxt] .. ".")
  else
    say("The porter silently hands you a card: THE HAT OF " .. string.upper(case.name(c, nxt)) .. " GOES ON PEG " .. lv.pegs[nxt] .. ".")
    case.wiggins("That is not your peg, it is " .. case.name(c, nxt) .. "'s! Let them know, quietly.")
  end
end

function pegs(player)
  local lv = deal()
  if lv.open then
    say("Every hat hangs on its peg.")
    return
  end
  local options = {}
  for i = 1, PEGS do
    options[i] = "Peg " .. i
  end
  local peg = ui.dialog("Which peg do you hang your hat on?", options)
  if not peg then
    return
  end
  local result = case.change(function(v)
    if v.open or not v.club then
      return "open"
    end
    v.hats = v.hats or {}
    v.hats[player.id] = peg
    for _, id in ipairs(v.club) do
      if not v.hats[id] then
        return "hung"
      end
    end
    for _, id in ipairs(v.club) do
      if v.hats[id] ~= v.pegs[id] then
        v.hats = nil
        return "wrong"
      end
    end
    v.open = true
    return "right"
  end)
  if result == "hung" then
    say("You hang your hat on peg " .. peg .. ".")
    case.news(player, player.name .. " hangs a hat on peg " .. peg .. ".")
  elseif result == "wrong" then
    local text = "The porter looks at the pegs, shakes his head and hands all the hats back. Somebody's hat was on the wrong peg."
    say(text)
    case.news(player, text)
  elseif result == "right" then
    local text = "The porter nods, and opens the door of the Strangers' Room. A large gentleman waves you in."
    say(text)
    case.news(player, text)
  end
  show(player)
end

function mycroft(player)
  local lv = case.lv()
  if not lv.open then
    return
  end
  if lv.dealt then
    say("Mycroft Holmes reads his newspaper. He has said all he will say.")
    return
  end
  -- the cards go to the Irregulars still on the case: those the porter
  -- had cards for, or else everyone active
  local c = case.get()
  local club = {}
  for _, id in ipairs(lv.club or {}) do
    if c.active[id] then
      club[#club + 1] = id
    end
  end
  if #club == 0 then
    club = case.active_ids(c)
  end
  -- the cards first, then dealt (nothing waits in between)
  for k = 1, 4 do
    if not case.give(club[(k - 1) % #club + 1], "evidence_" .. k) then
      return
    end
  end
  if not case.change(function(v)
        v.dealt = true
        return true
      end) then
    return
  end
  local text = "Mycroft Holmes, Sherlock's brother, whispers: \"The card in that crate was stolen from my coat. My brother is busy, so here is what he found.\" He hands out four evidence cards. Look in your satchels!"
  say(text)
  case.news(player, text)
  show(player)
end

function hint(player)
  local lv = case.lv()
  if not lv.club then
    case.wiggins("Everyone clicks the porter.")
  elseif not lv.open then
    case.wiggins("Your card tells someone else's peg. Pass the numbers on, then everyone hangs their hat.")
  elseif not lv.dealt then
    case.wiggins("Go to the gentleman in the Strangers' Room.")
  else
    case.wiggins("Everyone clicks the cab.")
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
