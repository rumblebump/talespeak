-- The first room of the demo: describe it, then let the player choose
-- until they find the nail.

function on_enter(player)
  room.describe()
  say("A guard leans against the bars, half asleep.")

  while true do
    local choice = ui.dialog("What do you do, " .. player.name .. "?", {
      "Ask the guard for water",
      "Search the straw",
      "Wait quietly",
    })
    if choice == 1 then
      say("The guard grunts and slides a cup of water through the bars.")
    elseif choice == 2 then
      say("Under the straw you find a bent nail. It might open the lock.")
      return
    else
      say("Time passes. Somewhere, a door slams.")
    end
  end
end
