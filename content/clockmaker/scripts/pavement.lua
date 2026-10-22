-- Where a fifth player waits: the case is for at most four Irregulars.
-- When the party goes on without someone, their seat is free, and a
-- player here can knock to join.

local case = require("case")

function on_enter(player)
  if not case.arrive(player) then
    return
  end
  room.describe()
  if #case.get().order < case.MAX then
    case.wiggins("There is a free seat on the case! Knock on the door to join.")
  else
    case.wiggins("Sorry, this case already has four Irregulars. Ask the game master for a world of your own, or wait here: if a seat comes free, you can knock.")
  end
  show()
end

function show()
  if #case.get().order < case.MAX then
    ui.items({ { id = "knock", label = "knock on the door of 221B" } })
  else
    ui.items({})
  end
end

function on_click(player, item)
  if item == "knock" then
    knock(player)
  end
end

function on_input(player, text)
  if case.has(string.lower(text), "knock") then
    knock(player)
  else
    say("Cabs rattle past. The case is upstairs, for the four Irregulars.")
  end
end

function knock(player)
  if #case.get().order >= case.MAX then
    say("Nobody opens. The case is full.")
    show()
    return
  end
  room.go(case.LEVELS[case.get().level].start)
end

function on_change(player, keys)
  show()
end

function on_leave(player)
  case.leave(player)
end
