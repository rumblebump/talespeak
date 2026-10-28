package script

import (
	"errors"
	"fmt"
	"time"
)

// What a script reaches the game through: Host for the player whose hook
// runs, Party for the others, and the values they pass (players, voices,
// items, dialogs).

// Host receives what a script sends to the player during a call.
type Host interface {
	Say(text string, how Voice)
	DescribeRoom()
	// Items replaces the things in the room the player can click.
	Items(items []Item)
	// Image shows one of the pack's images; it reports false when the pack
	// has no image of that name.
	Image(name, caption string) bool
	// Go moves the player to room once the hook ends; it reports false
	// when the pack has no such room.
	Go(room string) bool
	// Room is the id of the player's current room.
	Room() string
	// PlayerState is the player's state.player; nil when there is none.
	PlayerState() *State
	// WorldState is the world's state.world; nil when there is none.
	WorldState() *State
	// StartTimer starts the player's timer name, replacing one of that
	// name: once d has passed, the engine calls on_timer(player, name).
	// show, when not "", is a label the player sees with a countdown. An
	// error refuses it (too many timers); timer.start returns it.
	StartTimer(name string, d time.Duration, show string) error
	// StopTimer stops the player's timer name and reports whether it was
	// running.
	StopTimer(name string) bool
	// TimerLeft is the time until the player's timer name runs out, and
	// false when it is not running.
	TimerLeft(name string) (time.Duration, bool)
}

// Party is what a Host shares with the other players in its room and its
// world: who is there, and the room's timers. A Host without it plays
// alone, and room.players, world.players and room timers raise an error.
type Party interface {
	// Players lists the players in the room, the caller included, in the
	// order they came in.
	Players() []Player
	// WorldPlayers lists the players online in the world, in every room,
	// the caller included, in the order they joined.
	WorldPlayers() []Player
	// StartRoomTimer, StopRoomTimer and RoomTimerLeft are StartTimer,
	// StopTimer and TimerLeft for a timer of the room, shared by the
	// players in it: once it runs out, the engine calls on_timer(player,
	// name, "room") for one of them.
	StartRoomTimer(name string, d time.Duration, show string) error
	StopRoomTimer(name string) bool
	RoomTimerLeft(name string) (time.Duration, bool)
}

// Clock is what a Host tells of its world's time (T-0012). A Host without
// it has a world that never pauses and has no game time: world.paused()
// is false, world.time() is 0, and timer.start with game = true raises an
// error.
type Clock interface {
	// Paused reports whether a game master paused the world.
	Paused() bool
	// GameTime is how long the world has run unpaused with players in it.
	GameTime() time.Duration
	// StartGameTimer is StartTimer, or with room StartRoomTimer, for a
	// timer that counts game time instead of real time.
	StartGameTimer(name string, d time.Duration, show string, room bool) error
}

// Player is a player as room.players and world.players list them.
type Player struct {
	ID   string
	Name string
	// Room is the player's room; world.players lists it.
	Room string
	// GM is set for a game master of the world (player.gm).
	GM bool
}

// Voice says how a line reaches the player, set by say(text, {speak =
// true, show = false}): Speak reads it aloud even when the player has not
// turned read aloud on, Hide shows it only when it cannot be spoken.
type Voice struct {
	Speak bool
	Hide  bool
	// From is the speaker's name, such as a guide, shown with the line and
	// read aloud in another voice; "" is the story itself.
	From string
}

// Item is something the player can click, set with ui.items.
type Item struct {
	ID    string
	Label string
}

// Dialog is a question a paused hook waits on: ui.dialog, ui.select or
// ui.ask.
type Dialog struct {
	Title   string
	Options []string
	// Multiple is set by ui.select: the player picks Min to Max options.
	Multiple bool
	Min, Max int
	// Ask is set by ui.ask: the player types the answer, and Options is
	// empty. Strict asks the client for no typing help.
	Ask    bool
	Strict bool
	// Voice is how the title reaches the player.
	Voice Voice
	// Timeout, when not 0, is how long the player has to answer; then the
	// engine calls Expire, and the question returns nil.
	Timeout time.Duration
}

// answer is what a paused hook gets back: choices for ui.dialog and
// ui.select, text for ui.ask.
type answer struct {
	choices []int
	text    string
	expired bool // the question's timeout passed: it returns nil
}

// Check returns the player's answer to d in ascending order, or an error
// when it is not one d accepts: one option for ui.dialog, Min to Max
// different options for ui.select.
func (d *Dialog) Check(choices []int) ([]int, error) {
	n := len(d.Options)
	if d.Ask {
		return nil, errors.New("this question wants a typed answer")
	}
	if !d.Multiple {
		if len(choices) != 1 || choices[0] < 1 || choices[0] > n {
			return nil, errors.New("pick one of the options")
		}
		return []int{choices[0]}, nil
	}
	if len(choices) < d.Min || len(choices) > d.Max {
		return nil, fmt.Errorf("pick %d to %d options", d.Min, d.Max)
	}
	seen := make([]bool, n+1)
	for _, c := range choices {
		if c < 1 || c > n || seen[c] {
			return nil, errors.New("options must be different numbers of the dialog's options")
		}
		seen[c] = true
	}
	out := make([]int, 0, len(choices))
	for c := 1; c <= n; c++ {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out, nil
}
