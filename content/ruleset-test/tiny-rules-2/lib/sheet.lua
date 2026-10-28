-- The character sheet of Tiny Rules 2: level and courage in one table.
local sheet = {}

function sheet.stats()
  return state.profile.get("stats") or { level = 1, courage = 1 }
end

function sheet.level()
  return sheet.stats().level
end

return sheet
