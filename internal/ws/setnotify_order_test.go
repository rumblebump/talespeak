package ws

import (
	"sync"
	"sync/atomic"
	"testing"
)

// orderGame's sessions record a Handle that came before SetNotify.
type orderGame struct{ early *atomic.Int32 }

func (g orderGame) NewSession(string, Who) (Session, []Message) {
	return &orderSession{early: g.early}, nil
}

type orderSession struct {
	mu    sync.Mutex
	set   bool
	early *atomic.Int32
}

func (s *orderSession) SetNotify(func())           { s.mu.Lock(); s.set = true; s.mu.Unlock() }
func (s *orderSession) Pending() ([]Message, bool) { return nil, true }
func (s *orderSession) Close()                     {}
func (s *orderSession) Handle(Message) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set {
		s.early.Add(1)
	}
	return []Message{{Type: "text", Text: "ok"}}
}

// Pusher's contract: SetNotify comes before the first message is read.
func TestSetNotifyBeforeFirstMessage(t *testing.T) {
	integration(t)
	var early atomic.Int32
	_, url := newServer(t, WithGame(orderGame{&early}), WithConnectRate(1e6, 1e6))
	for range 200 {
		c := dial(t, url)
		writeText(t, c, []byte(`{"type":"input","text":"x"}`))
		read(t, c) // welcome
		read(t, c) // ok
		c.CloseNow()
	}
	if n := early.Load(); n > 0 {
		t.Errorf("%d of 200 connections handled a message before SetNotify", n)
	}
}
