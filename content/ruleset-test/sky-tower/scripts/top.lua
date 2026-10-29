-- The Tiny Rules 2 sheet: a character from Tiny Rules 1 arrives migrated.
local sheet = require("sheet")

function on_enter(player)
  room.describe()
  local s = sheet.stats()
  say("Level " .. s.level .. ", courage " .. s.courage .. ".")
end
