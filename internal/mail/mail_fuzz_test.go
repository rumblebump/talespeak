package mail

import (
	"net/mail"
	"strings"
	"testing"
	"time"
)

// FuzzValidAddress: an address ValidAddress takes is short, has no line
// break or space, and makes a mail whose header block ends where it
// should, with the address as its only recipient.
func FuzzValidAddress(f *testing.F) {
	for _, s := range []string{"mira@example.org", "a@b.c", "x\r\nBcc: y@z.org", "\"a b\"@c.org", "a@b@c.org", "<a@b.org>", "=?utf-8?q?x?=@a.org"} {
		f.Add(s)
	}
	from := &mail.Address{Name: "Talespeak", Address: "no-reply@example.org"}
	f.Fuzz(func(t *testing.T, addr string) {
		if ValidAddress(addr) != nil {
			return
		}
		if len(addr) > MaxAddress || strings.ContainsAny(addr, " \r\n\t") || strings.Count(addr, "@") != 1 {
			t.Fatalf("ValidAddress took %q", addr)
		}
		data, err := compose(from, Message{To: addr, Subject: "s", Body: "b"}, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		m, err := mail.ReadMessage(strings.NewReader(string(data)))
		if err != nil {
			t.Fatalf("%q: %v", addr, err)
		}
		to, err := m.Header.AddressList("To")
		if err != nil || len(to) != 1 || to[0].Address != addr || m.Header.Get("Bcc") != "" || m.Header.Get("Cc") != "" {
			t.Fatalf("%q: To = %v, %v", addr, to, err)
		}
	})
}
