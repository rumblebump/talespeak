-- Helping the fox finishes the story: it exports a flag for the stories
-- that continue this one, and the character gains a level.
local sheet = require("sheet")

function on_enter(player)
  room.describe()
  say("Forest Start " .. pack.version .. " on " .. pack.ruleset.id .. " " .. pack.ruleset.version .. ". Level " .. sheet.level() .. ".")
end

function on_click(player, item)
  if item == "fox" then
    progress.export("flags.rescued_fox", true)
    progress.complete()
    say("You free the fox. Level " .. sheet.level_up() .. ". Forest Start is finished.")
  end
end
