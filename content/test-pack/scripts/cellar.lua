-- Moving between rooms: on_enter does not describe the room, so the
-- client learns the cellar's items from the move alone.
local text = require("visits")
visits = 0

function on_enter(player)
  visits = visits + 1
  say(text.line("Cellar", visits, player.room))
end

function on_click(player, item)
  if item == "stairs" then
    say("Up the stairs.")
    room.go("hall")
  end
end
