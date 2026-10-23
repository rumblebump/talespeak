-- The case's secrets: what each world picks once (the desk word, the
-- clock times, the map, the crate, the thief, the evidence cards) and
-- reading the items that show them. Part of case.lua, which installs it.

local case, main = {}, nil
setmetatable(case, { __index = function(_, k) return main[k] end })

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

-- The module returns the function that adds these functions to the case
-- module; inside them, case.x finds the rest of the case there.
return function(m)
  main = m
  for k, f in pairs(case) do
    m[k] = f
  end
end
