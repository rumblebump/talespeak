-- Where a fifth player waits: the case is for at most four Irregulars.

local case = require("case")

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  case.wiggins("Sorry, this case already has four Irregulars. Ask the game master for a world of your own, or wait here and watch the cabs.")
  ui.items({})
end

function on_input(player, text)
  say("Cabs rattle past. The case is upstairs, for the four Irregulars.")
end

function on_leave(player)
  case.leave(player)
end
