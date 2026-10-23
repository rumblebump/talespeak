function on_enter(player)
  room.describe()
  say("Welcome, " .. player.name .. ".")
end

function on_click(player, item)
  if item == "lamp" then
    local n, label = ui.dialog("Light the lamp?", {"Yes", "No", "Maybe later"})
    say("Lamp answer: " .. n .. " " .. label)
  elseif item == "chest" then
    local nums, labels = ui.select("Take what?", {"rope", "coin", "map", "key"}, {min = 1, max = 2})
    say("Took: " .. table.concat(labels, "+") .. " (" .. table.concat(nums, ",") .. ")")
  elseif item == "painting" then
    ui.image("square.png", "A test square")
    ui.items({{id = "lamp", label = "lamp"}, {id = "painting", label = "seen painting"}})
  elseif item == "broken" then
    error("lever broke")
  elseif item == "riddle" then
    local a = ui.ask("What has keys but opens no locks?")
    say("Riddle: " .. tostring(words.same(a, "a piano") or words.same(a, "piano")) .. " " .. a)
  elseif item == "spelling" then
    local a = ui.ask("Write the word you hear.", {strict = true, speak = true, show = false})
    say("Spelling: " .. tostring(words.same(a, "castle")))
  elseif item == "voice" then
    say("Only heard, never read.", {speak = true, show = false})
    say("Heard and read.", {speak = true})
  elseif item == "door" then
    say("Down to the cellar.")
    room.go("cellar")
  elseif item == "trapdoor" then
    ui.dialog("Open the trapdoor?", {"Yes", "No"})
    error("the trapdoor is stuck")
  end
end

function on_input(player, text)
  if text == "guide" then
    -- A guide speaks apart from the story (T-0027).
    say("I am your guide.", { from = "Whisp" })
    local n, label = ui.dialog("Ready?", { "Yes", "No" }, { from = "Whisp" })
    say("Guide answer: " .. label)
    return
  end
  if text == "candle" then
    -- A long shown timer, which lasts through a reload (T-0012).
    timer.start("candle", 600, { show = "Candle" })
    return
  end
  if text == "hourglass" then
    -- A shown timer, and a question with a timeout (T-0029).
    timer.start("sand", 2, { show = "Sand" })
    local n = ui.dialog("Quick: left or right?", { "Left", "Right" }, { timeout = 1 })
    say(n and ("Went " .. n) or "Too slow.")
    return
  end
  say("You typed: " .. text)
end

function on_timer(player, name)
  say("The " .. name .. " ran out.")
end
