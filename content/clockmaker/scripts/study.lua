-- Level 1, 221B Baker Street. Holmes's telegram starts the case for the
-- Irregulars who are here. He tore the word that opens his desk into one
-- strip per Irregular and hid the strips: each player can find one, and
-- only together do they know the word. In the desk, his letter names the
-- levers that open the secret cupboard, one per Irregular, turned at the
-- same moment. The cupboard holds the key to the clock shop; the cab
-- outside is the save spot.

local case = require("case")

-- Where the strips are hidden: strip k is at SPOTS[k].
SPOTS = { "bookshelf", "slipper", "violin", "scuttle" }
SPOT_LABELS = {
  bookshelf = "bookshelf",
  slipper = "Persian slipper",
  violin = "violin case",
  scuttle = "coal scuttle",
}

-- The levers: the first n are the ones Holmes's letter names.
LEVERS = { "bust", "candlestick", "clock", "lamp" }
LEVER_LABELS = {
  bust = "bust of Napoleon",
  candlestick = "brass candlestick",
  clock = "clock on the mantelpiece",
  lamp = "green reading lamp",
}

HOLD_SECONDS = 10

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  local lv = case.lv()
  if not lv.n then
    case.wiggins("Mr Holmes sent for us, Irregulars! Read his telegram once everyone is here.")
  elseif lv.cupboard then
    case.wiggins("We have the key. To the cab, everyone!")
  end
  show(player)
end

function show(player)
  local lv = case.lv()
  local list = { { id = "telegram", label = "telegram on the table" } }
  if lv.n then
    for _, spot in ipairs(SPOTS) do
      list[#list + 1] = { id = spot, label = SPOT_LABELS[spot] }
    end
    list[#list + 1] = { id = "desk", label = lv.desk and "open writing desk" or "locked writing desk" }
  end
  if lv.desk then
    for _, lever in ipairs(LEVERS) do
      list[#list + 1] = { id = lever, label = LEVER_LABELS[lever] }
    end
  end
  if lv.cupboard then
    list[#list + 1] = { id = "cupboard", label = "secret cupboard" }
    list[#list + 1] = { id = "cab", label = "cab waiting outside (save spot)" }
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
  for _, word in ipairs({ "telegram", "desk", "cupboard", "cab" }) do
    if case.has(t, word) then
      return act(player, word)
    end
  end
  for _, spot in ipairs(SPOTS) do
    if case.has(t, spot) then
      return act(player, spot)
    end
  end
  for _, lever in ipairs(LEVERS) do
    if case.has(t, lever) then
      return act(player, lever)
    end
  end
  if case.has(t, "help") then
    say("Click what you see, or type: read the telegram, search the slipper, open the desk, turn the bust. Type satchel to see what you carry.")
  else
    say("Holmes would know what to do with \"" .. text .. "\". You do not, yet.")
  end
end

function act(player, item)
  local lv = case.lv()
  if item == "telegram" then
    return telegram(player)
  elseif item == "cab" then
    return cab(player)
  end
  if not lv.n then
    case.wiggins("First the telegram!")
    return
  end
  if SPOT_LABELS[item] then
    return search(player, item)
  elseif item == "desk" then
    return desk(player)
  elseif LEVER_LABELS[item] then
    return turn(player, item)
  elseif item == "cupboard" then
    return cupboard(player)
  end
end

function telegram(player)
  case.holmes("IRREGULARS. CLOCKMAKER ABERNETHY VANISHED. QUEEN'S JUBILEE WATCH STOLEN FROM HIS SHOP. MY NOTES ARE IN THE DESK. THE WORD IS TORN, ONE STRIP FOR EACH OF YOU. TRUST NO CLOCK. S.H.")
  if case.lv().n then
    return
  end
  local c = case.get()
  local ids = case.active_ids(c)
  local choice = ui.dialog("Start the case with " .. #ids .. " Irregular" .. (#ids == 1 and "" or "s") ..
    " (" .. case.names(c, ids) .. ")? Wait if someone is still coming.", {
      "Start the case",
      "Wait for the others",
    }, { from = "Wiggins" })
  if choice ~= 1 then
    case.wiggins("Right. Read the telegram again when everyone is here.")
    return
  end
  local n = case.freeze()
  case.change(function(lv)
    lv.strips = lv.strips or n
  end)
  local text = "The case begins for " .. n .. " Irregular" .. (n == 1 and "" or "s") ..
      ". Search the room: each of you can find one strip of Holmes's word."
  case.wiggins(text)
  case.news(player, text)
  show(player)
end

function search(player, spot)
  local lv = case.lv()
  local n = lv.strips or case.n()
  local k = 0
  for i, s in ipairs(SPOTS) do
    if s == spot then
      k = i
    end
  end
  if k > n then
    say("You search the " .. SPOT_LABELS[spot] .. ". Only Holmes's clutter.")
    return
  end
  local c = case.get()
  local got = case.change(function(v)
    v.found = v.found or {}
    if v.found[k] then
      return "taken"
    end
    for _, id in pairs(v.found) do
      if id == player.id and n > 1 then
        return "mine"
      end
    end
    v.found[k] = player.id
    return "found"
  end)
  if got == "taken" then
    say("Someone searched the " .. SPOT_LABELS[spot] .. " already: " .. case.name(c, (case.lv().found or {})[k]) .. " has that strip.")
  elseif got == "mine" then
    say("You have a strip already. Let another Irregular search here.")
  elseif got == "found" then
    if not case.give(player.id, "strip_" .. k) then
      return
    end
    say("In the " .. SPOT_LABELS[spot] .. " you find a torn strip of paper!")
    case.read(player, "strip_" .. k)
    case.wiggins("Only you can see your strip. Tell the others what it says!")
    case.news(player, player.name .. " found a strip of paper in the " .. SPOT_LABELS[spot] .. ".")
    show(player)
  end
end

function desk(player)
  local lv = case.lv()
  if lv.desk then
    letter()
    return
  end
  local word = case.word()
  local answer = ui.ask("The desk has a lock with " .. #word .. " letter wheels. What word do you turn?", { strict = true })
  if not answer then
    return
  end
  if not words.same(answer, word) then
    say("You turn the wheels to " .. string.upper(words.normalize(answer)) .. ". The desk stays locked.")
    case.wiggins("Put all the strips together, in the order of their numbers.")
    return
  end
  local first = case.change(function(v)
    local was = v.desk
    v.desk = true
    return not was
  end)
  if first == nil then
    return
  end
  if first then
    case.give(player.id, "lens")
    say("Click! The desk opens. Inside are a letter from Holmes and his magnifying glass, which you keep.")
    case.news(player, player.name .. " opened Holmes's desk!")
  end
  letter()
  show(player)
end

function letter()
  local n = case.n()
  local names = {}
  for i = 1, n do
    names[i] = "the " .. LEVER_LABELS[LEVERS[i]]
  end
  local how
  if n == 1 then
    how = "Turn " .. names[1] .. "."
  else
    how = "Each of you turns one of these at the same moment: " .. table.concat(names, ", ") .. "."
  end
  case.holmes("My secret cupboard hides the key to Abernethy's shop. " .. how)
end

function turn(player, lever)
  local lv = case.lv()
  if lv.cupboard then
    say("The cupboard is open already.")
    return
  end
  local n = case.n()
  local wanted = false
  for i = 1, n do
    if LEVERS[i] == lever then
      wanted = true
    end
  end
  if not wanted then
    say("You turn the " .. LEVER_LABELS[lever] .. ". Nothing happens.")
    return
  end
  local c = case.get()
  local got = case.change(function(v)
    v.pulls = v.pulls or {}
    if v.pulls[lever] then
      return "held"
    end
    for _, id in pairs(v.pulls) do
      if id == player.id then
        return "busy"
      end
    end
    v.pulls[lever] = player.id
    if case.count(v.pulls) >= n then
      v.cupboard = true
      v.pulls = nil
      return "open"
    end
    return "turned"
  end)
  if got == "held" then
    say(case.name(c, (case.lv().pulls or {})[lever]) .. " is holding the " .. LEVER_LABELS[lever] .. " already.")
  elseif got == "busy" then
    say("You can only hold one at a time. The others must turn the rest!")
  elseif got == "open" then
    timer.cancel("hold", { room = true })
    say("You turn the " .. LEVER_LABELS[lever] .. ". With a click, the bookshelf swings aside: a secret cupboard!")
    case.news(player, "With a click, the bookshelf swings aside: a secret cupboard!")
    show(player)
  elseif got == "turned" then
    say("You turn the " .. LEVER_LABELS[lever] .. " and hold it. Quick, the others!")
    case.news(player, player.name .. " turns the " .. LEVER_LABELS[lever] .. ". Quick!")
    if not timer.left("hold", { room = true }) then
      timer.start("hold", HOLD_SECONDS, { room = true, show = "Hold on!" })
    end
  end
end

function on_timer(player, name, kind)
  if name ~= "hold" or kind ~= "room" then
    return
  end
  local sprang = case.change(function(v)
    if v.cupboard or not v.pulls then
      return false
    end
    v.pulls = nil
    return true
  end)
  if sprang then
    say("Clack! The levers spring back. Turn them all at the same moment!")
    case.news(player, "Clack! The levers spring back. Turn them all at the same moment!")
  end
end

function cupboard(player)
  local c = case.get()
  local holder = case.holder("shop_key")
  if holder then
    say("The cupboard is empty now. " .. case.name(c, holder) .. " has the brass key.")
    return
  end
  if not case.give(player.id, "shop_key") then
    return
  end
  say("In the cupboard: a brass key with a label, ABERNETHY AND SON, CLERKENWELL. You put it in your satchel.")
  case.news(player, player.name .. " takes the brass key from the cupboard.")
end

function cab(player)
  local lv = case.lv()
  if not lv.cupboard or not case.holder("shop_key") then
    case.wiggins("Not yet! We need the key to the clock shop first.")
    return
  end
  case.ready(player)
end

function hint(player)
  local lv = case.lv()
  if not lv.n then
    case.wiggins("Read the telegram and start the case.")
  elseif not lv.desk then
    case.wiggins("Search the bookshelf, the slipper, the violin case and the coal scuttle. Read your strips to each other, in number order, and try the word on the desk.")
  elseif not lv.cupboard then
    case.wiggins("Read Holmes's letter in the desk. Each of you turns one of the things it names, quickly, one after the other.")
  else
    case.wiggins("Take the key from the cupboard, then everyone clicks the cab.")
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
