-- What happens when a player dies (T-0018), for any pack to copy. The
-- world's game master picks it: pack.yaml declares a "death" setting,
-- the lobby asks for it when a world is created, and /set death MODE
-- changes it while the world runs. Scripts read it with
-- state.game.get("death"):
--
--   stay     a dead player stays dead for the rest of the world
--   revive   a dead player waits until a spell or another script action
--            brings them back: death.revive(player_id)
--   respawn  the player wakes up again at once, at their last save point
--
-- pack.yaml:
--
--   settings:
--     - name: death
--       label: When a player dies
--       choices: [respawn, revive, stay]
--
-- In a room:
--
--   local death = require("death")
--   function on_enter(player)
--     if death.blocked(player) then return end -- dead: nothing else happens
--     death.save_point(player)                 -- a place to wake up again
--   end
--   function on_input(player, text)
--     if death.blocked(player) then return end
--     if text == "jump" then death.die(player, "You fall.") end
--   end
--   function on_change(player, keys) death.on_change(player, keys) end
--
-- Who is dead is in state.world, under "dead_<player id>", so another
-- player's script can revive them (state.player is the player's own).
-- The save point is the room id in state.player "save_point"; without
-- one, the player wakes up where they died. After a respawn,
-- death.respawned() is true once, in the room they wake up in, so
-- the room can start its puzzle over.
--
-- Only functions and locals here: a module also runs at world start, for
-- no player.

local death = {}

-- MODES are the choices of the setting; a world without the setting
-- (or with a value from elsewhere) behaves as DEFAULT.
local MODES = { stay = true, revive = true, respawn = true }
local DEFAULT = "stay"

-- texts are what the player reads; a pack changes them after require.
death.texts = {
  stay = "You are dead. In this world, the dead stay dead.",
  revive = "You are dead. Wait: someone may still bring you back.",
  respawn = "You wake up again, a little dizzy.",
  revived = "Warmth flows back into you. You are alive again!",
}

local function key(id)
  return "dead_" .. id
end

-- death.mode is the world's death setting now.
function death.mode()
  local m = state.game.get("death")
  if MODES[m] then
    return m
  end
  return DEFAULT
end

-- death.dead reports whether the player with this id is dead.
function death.dead(id)
  return state.world.get(key(id)) ~= nil
end

-- death.save_point makes room (default: the player's room) the place the
-- player wakes up in after dying in respawn mode. Returns what
-- state.player.set returns.
function death.save_point(player, room_id)
  return state.player.set("save_point", room_id or player.room)
end

-- respawn wakes the player up at their save point. room.go may not wait
-- after it, so the room's next question comes in on_enter.
local function respawn(player)
  state.player.set("respawned", true)
  say(death.texts.respawn)
  room.go(state.player.get("save_point") or player.room)
end

-- death.die kills the player, after saying text when given. It returns
-- the mode it used, or nil and the reason when the world cannot keep
-- that the player is dead.
function death.die(player, text)
  if text then
    say(text)
  end
  local mode = death.mode()
  if mode == "respawn" then
    respawn(player)
    return mode
  end
  local ok, err = state.world.set(key(player.id), mode)
  if not ok then
    return nil, err
  end
  ui.items({})
  say(death.texts[mode])
  return mode
end

-- death.blocked returns true when the player is dead, after telling them
-- so: the room then does nothing else. When the game master switched
-- the world to respawn meanwhile, the player wakes up instead.
function death.blocked(player)
  if not death.dead(player.id) then
    return false
  end
  if death.mode() == "respawn" and state.world.set(key(player.id), nil) then
    respawn(player)
    return true
  end
  ui.items({})
  say(death.texts[state.world.get(key(player.id))] or death.texts.stay)
  return true
end

-- death.revive brings the player with this id back to life, unless the
-- world's dead stay dead. It returns true, or nil and the reason.
function death.revive(id)
  if not death.dead(id) then
    return nil, "not dead"
  end
  if death.mode() == "stay" then
    return nil, "in this world, the dead stay dead"
  end
  return state.world.set(key(id), nil)
end

-- death.on_change, called from the room's on_change, tells a player who
-- was revived and shows them their room again. It returns whether it
-- did.
function death.on_change(player, keys)
  local mine = key(player.id)
  for _, k in ipairs(keys) do
    if k == mine and not death.dead(player.id) then
      say(death.texts.revived)
      room.go(player.room)
      return true
    end
  end
  return false
end

-- death.respawned is true once after a respawn, in the room the player
-- woke up in.
function death.respawned()
  if not state.player.get("respawned") then
    return false
  end
  state.player.set("respawned", nil)
  return true
end

return death
