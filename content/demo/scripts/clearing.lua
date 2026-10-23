-- The forest clearing behind the guard room's back door: the room of the
-- demo the players share. Everyone who escapes meets here.
--
-- The things by the fire follow the take rules of lib/items.lua: the
-- purse is split among the players here when the first one opens it,
-- everyone gets one apple, the horn goes to whoever takes it first (and
-- back by the fire when they leave the world), and there are three
-- torches. The shrine blesses a player for a minute or five moves.
--
-- The dice are a game in turns (lib/round.lua): whoever picks them up
-- starts a round for everyone here, in the order they came; each player
-- rolls once and ends their turn, with 30 seconds for it. The highest
-- roll wins, and the blessing adds one. A game master can skip a turn,
-- set the order or stop the turns with /skip, /turns and /free.

require("rooms") -- whisp: lib/rooms.lua
local items = require("items")
local round = require("round")

items.define({
  { id = "purse", rule = "split", amount = 12, gives = "silver" },
  { id = "apples", rule = "each", gives = "apple" },
  { id = "horn", rule = "once" },
  { id = "torches", rule = "count", count = 3, gives = "torch" },
})

THINGS = {
  { id = "purse", label = "guard's purse with twelve silver coins" },
  { id = "apples", label = "basket of apples" },
  { id = "horn", label = "old hunting horn" },
  { id = "torches", label = "bundle of three torches" },
  { id = "shrine", label = "little stone shrine" },
  { id = "dice", label = "dice by the fire" },
  { id = "path", label = "path home" },
}

TURN_SECONDS = 30
BLESSING = "blessing"

ended = false      -- the player went home: the story is over
news_seen = 0      -- the last clearing news this player heard
dice_seen = 0      -- the last dice news this player heard

function on_enter(player)
  room.describe()
  local others = {}
  for _, p in ipairs(room.players()) do
    if p.id ~= player.id then
      others[#others + 1] = p.name
    end
  end
  if #others > 0 then
    say("By the fire sit " .. table.concat(others, ", ") .. ".")
  else
    say("Nobody else is here yet. Others may come along the path.")
  end
  whisp("Free at last! Take what you need, and try the dice when others are here.")
  news(player.name .. " comes out of the forest.")
  show_items(player)
end

-- news tells the other players in the clearing something, through
-- state.world: they hear it in on_change.
function news(text)
  state.world.update("clearing_news", function(v)
    return { n = (v and v.n or 0) + 1, text = text }
  end)
end

function on_change(player, keys)
  local n = state.world.get("clearing_news")
  if n and n.n > news_seen then
    news_seen = n.n
    say(n.text)
  end
  local d = state.world.get("dice")
  if d and d.n > dice_seen then
    dice_seen = d.n
    say(d.text)
  end
  if round.changed(player, keys) then
    local current = round.current(player)
    if current and current ~= player.id then
      say(round.status(player))
    end
  end
  if not ended then
    show_items(player)
  end
end

function on_leave(player)
  round.leave(player)
  if items.release(player, "horn") then
    news(player.name .. " has gone. The old horn lies by the fire again.")
  else
    news(player.name .. " has gone.")
  end
end

-- The game master's turn commands, /skip, /turns and /free (lib/round.lua).
commands = round.commands

function on_command(player, name, args)
  round.command(player, name, args)
end

-- /reset room by the game master: the dice game stops. The things by the
-- fire stay as they were taken.
function on_reset(player)
  round.free(player)
end

function on_timer(player, name, kind)
  if items.on_timer(player, name) then
    return
  end
  round.timeout(player, name, kind)
end

function on_turn(player)
  say("Your turn! Click the dice to roll, then end your turn.")
  show_items(player)
end

function on_round_end(player, n)
  local d = state.world.get("dice") or {}
  local best, winners = 0, {}
  for _, r in ipairs(d.rolls or {}) do
    if r.total > best then
      best, winners = r.total, { r.name }
    elseif r.total == best then
      winners[#winners + 1] = r.name
    end
  end
  local text
  if #winners == 0 then
    text = "Nobody rolled. The dice go back by the fire."
  elseif #winners == 1 then
    text = winners[1] .. " wins the dice with " .. best .. "!"
  else
    text = table.concat(winners, " and ") .. " tie with " .. best .. "."
  end
  dice_news(text, {})
  say(text)
  round.free(player)
  show_items(player)
end

-- dice_news keeps the rolls of the game and tells everyone text.
function dice_news(text, rolls)
  state.world.update("dice", function(v)
    return { n = (v and v.n or 0) + 1, text = text, rolls = rolls }
  end)
end

function show_items(player)
  local list = items.visible(player, THINGS)
  if round.current(player) == player.id then
    list[#list + 1] = { id = "done", label = "end your turn" }
  end
  ui.items(list)
end

function on_click(player, item)
  act(player, item)
end

function on_input(player, text)
  if ended then
    act(player, "")
    return
  end
  local t = " " .. words.normalize(text) .. " "
  local function has(w)
    return string.find(t, w, 1, true) ~= nil
  end
  if has("done") or has(" end ") or has("pass") then
    act(player, "done")
  elseif has("purse") or has("silver") or has("coin") then
    act(player, "purse")
  elseif has("apple") then
    act(player, "apples")
  elseif has("horn") then
    act(player, "horn")
  elseif has("torch") then
    act(player, "torches")
  elseif has("shrine") or has("pray") then
    act(player, "shrine")
  elseif has("dice") or has("roll") then
    act(player, "dice")
  elseif has("path") or has("home") then
    act(player, "path")
  elseif has("inventory") or has(" bag ") or has("carry") then
    act(player, "bag")
  else
    say("Try: take the purse, an apple, the horn or a torch, pray at the shrine, roll the dice, look in your bag, or follow the path home.")
  end
end

function act(player, what)
  if ended then
    say("The story has ended. Reload the page to play again.")
    return
  end
  items.move(player)
  if what == "dice" then
    dice(player)
  elseif what == "done" then
    done(player)
  elseif what == "shrine" then
    shrine(player)
  elseif what == "path" then
    home(player)
  elseif what == "bag" then
    bag(player)
  else
    take(player, what)
  end
  if not ended then
    show_items(player)
  end
end

TAKEN = {
  purse = function(got) return "You take your share from the purse: " .. got.n .. " silver." end,
  apples = function() return "You take a red apple." end,
  horn = function() return "You take the old hunting horn. It still blows!" end,
  torches = function() return "You take a torch." end,
}

NOT_TAKEN = {
  purse = { late = "The coins were shared before you came.", had = "You have your share.", none = "There were not enough coins for everyone." },
  apples = { had = "You have your apple already." },
  horn = { gone = "took the old horn already." },
  torches = { gone = "The torches are all gone." },
}

function take(player, id)
  local got, why, who = items.take(player, id)
  if got then
    say(TAKEN[id](got))
    return
  end
  if why == "busy" then
    say("(The game cannot remember that right now. Try again in a moment.)")
  elseif id == "horn" and why == "gone" then
    say((who or "Someone") .. " " .. NOT_TAKEN.horn.gone)
  elseif id == "purse" and why == "late" and items.count(player, "silver") > 0 then
    say(NOT_TAKEN.purse.had)
  else
    say(NOT_TAKEN[id] and NOT_TAKEN[id][why] or "You cannot take that.")
  end
end

function shrine(player)
  if items.blessed(player, BLESSING) then
    say("The shrine glows softly. Its blessing is still on you.")
    return
  end
  if items.bless(player, BLESSING, { seconds = 60, moves = 5, show = "Forest's blessing" }) then
    say("You kneel at the shrine. A warm light settles on you: one more on your dice for a minute, or five moves.")
  else
    say("(The game cannot remember that right now. Try again in a moment.)")
  end
end

function on_effect_end(player, id)
  if id == BLESSING then
    say("The warm light of the shrine fades.")
  end
end

function dice(player)
  local current = round.current(player)
  if not current then
    dice_news(player.name .. " picks up the dice: a game for everyone here! Highest roll wins.", {})
    say("You pick up the dice: a game for everyone here! Highest roll wins.")
    round.turns(player, nil, { seconds = TURN_SECONDS })
    say(round.status(player) or "")
    return
  end
  if not round.may_act(player) then
    return
  end
  local d = state.world.get("dice") or {}
  for _, r in ipairs(d.rolls or {}) do
    if r.id == player.id then
      say("You rolled already. End your turn.")
      return
    end
  end
  local a, b = math.random(1, 6), math.random(1, 6)
  local bonus = items.blessed(player, BLESSING) and 1 or 0
  local total = a + b + bonus
  local text = player.name .. " rolls " .. a .. " and " .. b
  if bonus > 0 then
    text = text .. ", blessed: " .. total .. "."
  else
    text = text .. ": " .. total .. "."
  end
  local rolls = d.rolls or {}
  rolls[#rolls + 1] = { id = player.id, name = player.name, total = total }
  dice_news(text, rolls)
  say("You roll " .. a .. " and " .. b .. (bonus > 0 and ", and the blessing adds one" or "") .. ": " .. total .. ".")
end

function done(player)
  local ok, why = round.done(player)
  if ok then
    say("You end your turn.")
  elseif why == "not your turn" then
    round.may_act(player)
  else
    say("There is no game going on.")
  end
end

function bag(player)
  local list = items.inventory(player)
  if #list == 0 then
    say("Your bag is empty.")
    return
  end
  local parts = {}
  for _, e in ipairs(list) do
    parts[#parts + 1] = e.n .. " " .. e.name
  end
  say("You carry: " .. table.concat(parts, ", ") .. ".")
end

function home(player)
  round.leave(player)
  say("You follow the path through the dark forest until you see the lights of home. You made it!")
  ended = true
  ui.items({})
  say("THE END. Reload the page to play again.")
end
