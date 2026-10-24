-- What came over from Forest Start is in state.player.
local sheet = require("sheet")

function on_enter(player)
  room.describe()
  if state.player.get("flags.rescued_fox") then
    say("The fox you rescued runs ahead of you.")
  end
  say("Level " .. sheet.level() .. ".")
end
