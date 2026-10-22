-- Level 5, Scotland Yard: the finale. Four people were near the shop on
-- the night of the theft. The evidence cards from the club clear three of
-- them, but every Irregular holds only some of the cards, so they compare
-- them (by talking) and accuse the thief together: the accusation counts
-- only when all agree. A wrong one costs time while Lestrade checks.

local case = require("case")

CHECK_SECONDS = 30

ENDINGS = {
  "Tobias Finch hid the watch in his toolbox. He wanted to show his master he could make a better one.",
  "Silas Crane never went to Brighton: the hotel porter is his cousin. The watch was sewn into his coat.",
  "Mrs Pell left the opera after the first song. The watch was in her hatbox, under a very large hat.",
  "Mr Dunn rang the bell every hour, but in between he had plenty of time. The watch was in his lantern.",
}

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  if case.get().done then
    ending()
    return
  end
  case.wiggins("This is it, Irregulars. Look at the photographs, read your evidence cards to each other, then accuse the thief together.")
  show(player)
end

function show(player)
  if case.get().done then
    case.items({})
    return
  end
  local list = { { id = "lestrade", label = "Inspector Lestrade" } }
  for i, s in ipairs(case.SUSPECTS) do
    list[#list + 1] = { id = "photo" .. i, label = "photograph of " .. s.name }
  end
  list[#list + 1] = { id = "accuse", label = "accuse the thief" }
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
  if case.has(t, "accuse") or case.has(t, "thief") then
    act(player, "accuse")
  elseif case.has(t, "lestrade") then
    act(player, "lestrade")
  else
    say("Try: talk to Lestrade, accuse the thief. Type satchel to read your evidence cards.")
  end
end

function act(player, item)
  if case.get().done then
    ending()
    return
  end
  if item == "lestrade" then
    say("\"Four people were near the shop that night,\" says Lestrade. \"Which one did it? You must all agree, mind. I have no time for guesses.\"")
  elseif string.sub(item, 1, 5) == "photo" then
    local s = case.SUSPECTS[tonumber(string.sub(item, 6))]
    if s then
      say("The photograph shows " .. s.name .. ".")
    end
  elseif item == "accuse" then
    accuse(player)
  end
end

function accuse(player)
  local left = timer.left("check", { room = true })
  if left then
    say("Lestrade is still checking your last guess. Wait " .. math.ceil(left) .. " seconds.")
    return
  end
  local options = {}
  for i, s in ipairs(case.SUSPECTS) do
    options[i] = s.name
  end
  local pick = ui.dialog("Who stole the Queen's watch?", options)
  if not pick then
    return
  end
  local c = case.get()
  local ids = case.active_ids(c)
  local result = case.change(function(v)
    v.votes = v.votes or {}
    v.votes[player.id] = pick
    for _, id in ipairs(ids) do
      if not v.votes[id] then
        return "waiting"
      end
    end
    for _, id in ipairs(ids) do
      if v.votes[id] ~= pick then
        return "split"
      end
    end
    v.votes = nil
    return "agreed"
  end)
  if not result then
    return
  end
  local name = case.SUSPECTS[pick].name
  if result == "waiting" then
    say("You point at " .. name .. ". Lestrade waits for the others.")
    case.news(player, player.name .. " accuses " .. name .. ".")
  elseif result == "split" then
    local votes = case.lv().votes or {}
    local who = {}
    for _, id in ipairs(ids) do
      who[#who + 1] = case.name(c, id) .. ": " .. case.SUSPECTS[votes[id]].name
    end
    local text = "You do not agree yet. " .. table.concat(who, "; ") .. ". Talk it over and accuse again."
    say(text)
    case.news(player, text)
  elseif pick ~= case.thief() then
    timer.start("check", CHECK_SECONDS, { room = true, show = "Lestrade checks" })
    local text = "Lestrade sends a constable to check " .. name .. "... It was not them! Look at the evidence again."
    say(text)
    case.news(player, text)
  else
    solved(player)
  end
end

function solved(player)
  local ok = state.world.update("case", function(v)
    v.done = true
    return v
  end)
  if not ok then
    say("(The game cannot remember that right now. Try again in a moment.)")
    return
  end
  case.news(player, "The case is solved!")
  ending()
end

shown = false -- this player saw the ending in this room

function ending()
  if shown then
    say("The case is closed. Well done, Irregulars!")
    return
  end
  shown = true
  local thief = case.SUSPECTS[case.thief()]
  say("Lestrade's constables come back with " .. thief.name .. ", and the Queen's jubilee watch! " .. ENDINGS[case.thief()])
  say("Mr Abernethy is found safe, locked in his own cellar, and the door bursts open: Sherlock Holmes himself, smiling. \"Well done, Irregulars. I could not have done it better.\"", { from = "Holmes" })
  say("THE END. The case is closed.")
  case.items({})
end

function hint(player)
  case.wiggins("Each evidence card clears one person. Read all your cards to each other: the one nobody clears is the thief. Then everyone accuses the same person.")
end

function on_change(player, keys)
  if case.changed(player, keys) then
    return
  end
  if case.get().done then
    if not shown then
      ending()
    end
    return
  end
  show(player)
end

function on_leave(player)
  case.leave(player)
end
