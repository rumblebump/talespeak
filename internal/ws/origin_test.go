package ws

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Origins that look like the server's own but are not, and malformed
// ones, are all refused with 403 before the upgrade.
func TestCrossOriginLookalikesRefused(t *testing.T) {
	integration(t)
	_, url := newServer(t)
	host := strings.TrimPrefix(url, "ws://") // 127.0.0.1:port
	for _, origin := range []string{
		"http://" + host + ".evil.example",      // the host as a prefix
		"http://evil." + host,                   // the host as a suffix
		"http://evil.example/" + host,           // the host in the path
		"http://evil.example?" + host,           // the host in the query
		"http://" + strings.Split(host, ":")[0], // same host, no port
		"http://",                               // no host
		"file://",                               // local files
		"http://[::1" + host,                    // does not parse
		"%zz",                                   // does not parse
		"chrome-extension://abcdef",             // an extension
	} {
		t.Run(origin, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
				HTTPHeader: http.Header{"Origin": {origin}},
			})
			if err == nil {
				c.CloseNow()
				t.Fatal("upgrade succeeded")
			}
			if resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Errorf("response = %v, want 403", resp)
			}
		})
	}
}
