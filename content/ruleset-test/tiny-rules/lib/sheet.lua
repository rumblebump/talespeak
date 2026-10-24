-- The character sheet of Tiny Rules 1: one number, the level, kept in
-- state.profile so it goes with the character from story to story.
local sheet = {}

function sheet.level()
  return state.profile.get("level") or 1
end

function sheet.level_up()
  local n = sheet.level() + 1
  state.profile.set("level", n)
  return n
end

return sheet
