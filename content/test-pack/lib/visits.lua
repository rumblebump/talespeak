-- A shared module: require("visits") in any room script returns this
-- table, run once per player and room.
local visits = {}

function visits.line(place, n, room)
  return place .. " visit " .. n .. ", in " .. room .. "."
end

return visits
