package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/rumblebump/talespeak/internal/pack"
)

func assetPack(t *testing.T) *pack.Pack {
	t.Helper()
	p, err := pack.Load(fstest.MapFS{
		"pack.yaml":    {Data: []byte("id: t\nversion: 1.0.0\ntitle: T\nstart: r\n")},
		"rooms/r.yaml": {Data: []byte("id: r\ntitle: R\ndescription: d\n")},
		"assets/a.png": {Data: []byte("\x89PNG\r\n\x1a\n0000")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Options.Assets serves GET /packs/; the engine's security headers stay,
// and an image gets its own stricter policy.
func TestAssetsRoute(t *testing.T) {
	h := New(Options{Version: "v", Assets: pack.AssetHandler(assetPack(t))})
	tests := []struct {
		method, path string
		code         int
		csp          string
	}{
		{http.MethodGet, "/packs/t/1.0.0/assets/a.png", http.StatusOK, "default-src 'none'; sandbox"},
		{http.MethodHead, "/packs/t/1.0.0/assets/a.png", http.StatusOK, "default-src 'none'; sandbox"},
		{http.MethodGet, "/packs/t/1.0.0/assets/b.png", http.StatusNotFound, ContentSecurityPolicy},
		{http.MethodGet, "/packs/x/1.0.0/assets/a.png", http.StatusNotFound, ContentSecurityPolicy},
		{http.MethodGet, "/packs/", http.StatusNotFound, ContentSecurityPolicy},
		{http.MethodGet, "/packs", http.StatusTemporaryRedirect, ContentSecurityPolicy}, // to /packs/, a 404
		{http.MethodPost, "/packs/t/1.0.0/assets/a.png", http.StatusMethodNotAllowed, ContentSecurityPolicy},
		{http.MethodPut, "/packs/t/1.0.0/assets/a.png", http.StatusMethodNotAllowed, ContentSecurityPolicy},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.code {
				t.Errorf("status = %d, want %d", rec.Code, tt.code)
			}
			hd := rec.Header()
			if got := hd.Get("Content-Security-Policy"); got != tt.csp {
				t.Errorf("CSP = %q, want %q", got, tt.csp)
			}
			for name, want := range map[string]string{
				"X-Content-Type-Options":       "nosniff",
				"Cross-Origin-Resource-Policy": "same-origin",
				"Cross-Origin-Embedder-Policy": "require-corp",
				"Access-Control-Allow-Origin":  "",
			} {
				if got := hd.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if tt.code == http.StatusOK && hd.Get("Content-Type") != "image/png" {
				t.Errorf("Content-Type = %q", hd.Get("Content-Type"))
			}
		})
	}
}

// Without Options.Assets nothing is served under /packs/.
func TestNoAssetsRoute(t *testing.T) {
	rec := get(t, "/packs/t/1.0.0/assets/a.png")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
