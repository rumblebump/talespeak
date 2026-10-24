-- Moves a Tiny Rules 1 sheet to 2: the level goes into stats, and every
-- character starts with courage 1.
function migrate(sheet)
  local level = sheet.level or 1
  sheet.level = nil
  sheet.stats = { level = level, courage = 1 }
  return sheet
end
