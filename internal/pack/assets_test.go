package pack

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// Image bytes that http.DetectContentType recognizes, padded to a size.
var magic = map[string]string{
	".png":  "\x89PNG\r\n\x1a\n",
	".jpg":  "\xff\xd8\xff\xe0",
	".jpeg": "\xff\xd8\xff\xe0",
	".gif":  "GIF89a",
	".webp": "RIFF\x00\x00\x00\x00WEBPVP8 ",
}

func image(ext string, size int) []byte {
	b := []byte(magic[ext])
	if size > len(b) {
		b = append(b, make([]byte, size-len(b))...)
	}
	return b
}

// packFS is the minimal pack plus files.
func packFS(files map[string]*fstest.MapFile) fstest.MapFS {
	fsys := fsWith(nil)
	for name, f := range files {
		fsys[name] = f
	}
	return fsys
}

func file(b []byte) *fstest.MapFile { return &fstest.MapFile{Data: b} }

func TestLoadAssets(t *testing.T) {
	p, err := Load(packFS(map[string]*fstest.MapFile{
		"assets/a.png":     file(image(".png", 100)),
		"assets/b-1_x.jpg": file(image(".jpg", 100)),
		"assets/c.jpeg":    file(image(".jpeg", 100)),
		"assets/d.gif":     file(image(".gif", 100)),
		"assets/e.webp":    file(image(".webp", 100)),
		"assets/9.png":     file(image(".png", 10)),
		"assets/" + strings.Repeat("n", 64) + ".png": file(image(".png", 10)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"a.png": "image/png", "b-1_x.jpg": "image/jpeg", "c.jpeg": "image/jpeg", "d.gif": "image/gif", "e.webp": "image/webp",
		"9.png": "image/png", strings.Repeat("n", 64) + ".png": "image/png",
	}
	if len(p.Assets) != len(want) {
		t.Errorf("assets = %d, want %d", len(p.Assets), len(want))
	}
	for name, typ := range want {
		a := p.Assets[name]
		if a == nil || a.Name != name || a.Type != typ || len(a.Data) == 0 || a.etag == "" {
			t.Errorf("asset %q = %+v, want type %s", name, a, typ)
		}
	}
	if url, ok := p.Image("a.png"); !ok || url != "/packs/t/1.0.0/assets/a.png" {
		t.Errorf("Image(a.png) = %q, %v", url, ok)
	}
	for _, name := range []string{"missing.png", "", "../pack.yaml", "A.png"} {
		if url, ok := p.Image(name); ok || url != "" {
			t.Errorf("Image(%q) = %q, %v; want nothing", name, url, ok)
		}
	}
}

func TestLoadWithoutAssets(t *testing.T) {
	p, err := Load(fsWith(nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Assets == nil || len(p.Assets) != 0 {
		t.Errorf("assets = %v, want an empty map", p.Assets)
	}
	// An empty folder is fine too.
	p, err = Load(packFS(map[string]*fstest.MapFile{"assets": {Mode: fs.ModeDir | 0o755}}))
	if err != nil || len(p.Assets) != 0 {
		t.Errorf("empty assets/: %v, %v", p, err)
	}
}

func TestLoadAssetErrors(t *testing.T) {
	png := file(image(".png", 64))
	tests := []struct {
		name  string
		files map[string]*fstest.MapFile
		want  []string
	}{
		{"upper case", map[string]*fstest.MapFile{"assets/Map.png": png}, []string{"assets/Map.png", "name must be"}},
		{"upper extension", map[string]*fstest.MapFile{"assets/map.PNG": png}, []string{"assets/map.PNG", "name"}},
		{"no base name", map[string]*fstest.MapFile{"assets/.png": png}, []string{"assets/.png", "name"}},
		{"hidden", map[string]*fstest.MapFile{"assets/.map.png": png}, []string{"assets/.map.png", "name"}},
		{"leading dash", map[string]*fstest.MapFile{"assets/-map.png": png}, []string{"assets/-map.png", "name"}},
		{"leading underscore", map[string]*fstest.MapFile{"assets/_map.png": png}, []string{"name"}},
		{"space", map[string]*fstest.MapFile{"assets/a map.png": png}, []string{"name"}},
		{"two dots", map[string]*fstest.MapFile{"assets/a.b.png": png}, []string{"name"}},
		{"double extension", map[string]*fstest.MapFile{"assets/a.png.exe": png}, []string{"name"}},
		{"65 characters", map[string]*fstest.MapFile{"assets/" + strings.Repeat("n", 65) + ".png": png}, []string{"name"}},
		{"svg", map[string]*fstest.MapFile{"assets/a.svg": file([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))}, []string{"assets/a.svg", "name"}},
		{"html", map[string]*fstest.MapFile{"assets/a.html": file([]byte("<html></html>"))}, []string{"name"}},
		{"no extension", map[string]*fstest.MapFile{"assets/readme": file([]byte("hi"))}, []string{"name"}},
		{"unicode", map[string]*fstest.MapFile{"assets/kärta.png": png}, []string{"name"}},
		{"folder", map[string]*fstest.MapFile{"assets/sub/a.png": png}, []string{"assets/sub", "no folders or links"}},
		{"folder named like an image", map[string]*fstest.MapFile{"assets/a.png/b.png": png}, []string{"assets/a.png", "no folders"}},
		{"symlink", map[string]*fstest.MapFile{"assets/a.png": {Data: []byte("../pack.yaml"), Mode: fs.ModeSymlink}}, []string{"assets/a.png", "links"}},
		{"assets is a file", map[string]*fstest.MapFile{"assets": file([]byte("x"))}, []string{"assets"}},
		{"gif named png", map[string]*fstest.MapFile{"assets/a.png": file(image(".gif", 64))}, []string{"assets/a.png", "image/gif", "not image/png"}},
		{"png named jpg", map[string]*fstest.MapFile{"assets/a.jpg": png}, []string{"assets/a.jpg", "not image/jpeg"}},
		{"html named png", map[string]*fstest.MapFile{"assets/a.png": file([]byte("<!DOCTYPE html><script>alert(1)</script>"))}, []string{"assets/a.png", "text/html"}},
		{"svg named png", map[string]*fstest.MapFile{"assets/a.png": file([]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`))}, []string{"assets/a.png", "not image/png"}},
		{"empty file", map[string]*fstest.MapFile{"assets/a.png": file(nil)}, []string{"assets/a.png", "not image/png"}},
		{"too large", map[string]*fstest.MapFile{"assets/a.png": file(image(".png", int(DefaultLimits.AssetBytes)+1))}, []string{"assets/a.png", "larger than"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Load(packFS(tt.files))
			if err == nil {
				t.Fatalf("Load = %+v, want an error", p.Assets)
			}
			if p != nil {
				t.Error("Load returned a pack with the error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// A real symlink on disk, as an admin's TALESPEAK_PACK folder could hold,
// is refused, also when it points at a valid image.
func TestLoadAssetSymlinkOnDisk(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pack.yaml", []byte(goodPack))
	write("rooms/r.yaml", []byte(goodRoom))
	write("assets/real.png", image(".png", 64))
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, image(".png", 64), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "assets", "link.png")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := os.Symlink("real.png", filepath.Join(dir, "assets", "inside.png")); err != nil {
		t.Fatal(err)
	}
	_, err := Load(os.DirFS(dir))
	if err == nil {
		t.Fatal("Load with symlinks in assets/ = nil error")
	}
	for _, want := range []string{"assets/link.png", "assets/inside.png"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "real.png") {
		t.Errorf("the regular file was refused: %v", err)
	}
}

func TestLoadAssetLimits(t *testing.T) {
	limits := Limits{AssetBytes: 1000, AssetsBytes: 2500}
	tests := []struct {
		name  string
		sizes []int
		want  string // "" for no error
	}{
		{"one at the limit", []int{1000}, ""},
		{"one over", []int{1001}, "larger than 1000 bytes"},
		{"total at the limit", []int{1000, 1000, 500}, ""},
		{"total over", []int{1000, 1000, 501}, "more than 2500 bytes in all"},
		{"many small over the total", []int{600, 600, 600, 600, 600}, "more than 2500 bytes in all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]*fstest.MapFile{}
			for i, n := range tt.sizes {
				files["assets/a"+strconv.Itoa(i)+".png"] = file(image(".png", n))
			}
			p, err := LoadLimits(packFS(files), limits)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Assets) != len(tt.sizes) {
					t.Errorf("assets = %d, want %d", len(p.Assets), len(tt.sizes))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadTooManyAssets(t *testing.T) {
	files := map[string]*fstest.MapFile{}
	for i := range MaxAssets + 1 {
		files["assets/a"+strconv.Itoa(i)+".png"] = file(image(".png", 16))
	}
	if _, err := Load(packFS(files)); err == nil || !strings.Contains(err.Error(), "at most 500") {
		t.Errorf("err = %v, want too many files", err)
	}
	delete(files, "assets/a0.png")
	if p, err := Load(packFS(files)); err != nil || len(p.Assets) != MaxAssets {
		t.Errorf("%d files: err = %v", MaxAssets, err)
	}
}

// Asset problems are reported with the pack's other problems.
func TestAssetErrorsJoinOthers(t *testing.T) {
	_, err := Load(packFS(map[string]*fstest.MapFile{
		"pack.yaml":     file([]byte("id: BAD\nversion: 1.0.0\ntitle: T\nstart: r\n")),
		"assets/A.png":  file(image(".png", 16)),
		"assets/b.png":  file(image(".gif", 16)),
		"assets/ok.png": file(image(".png", 16)),
	}))
	if err == nil {
		t.Fatal("Load = nil error")
	}
	for _, want := range []string{"pack.yaml: id", "assets/A.png: name", "assets/b.png: content"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

func TestRoomItems(t *testing.T) {
	p, err := Load(fsWith(map[string]*string{
		"rooms/r.yaml": s(goodRoom + "items:\n  - id: straw\n    label: heap of straw\n  - id: door-2\n    label: \"" + strings.Repeat("é", MaxLabelBytes/2) + "\"\n"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	items := p.Rooms["r"].Items
	if len(items) != 2 || items[0] != (Item{ID: "straw", Label: "heap of straw"}) || items[1].ID != "door-2" || len(items[1].Label) != MaxLabelBytes {
		t.Errorf("items = %+v", items)
	}
	// An empty list is no items.
	if p, err := Load(fsWith(map[string]*string{"rooms/r.yaml": s(goodRoom + "items: []\n")})); err != nil || len(p.Rooms["r"].Items) != 0 {
		t.Errorf("items: []: %v", err)
	}
	// 20 items load.
	var many strings.Builder
	many.WriteString(goodRoom + "items:\n")
	for i := range MaxItems {
		many.WriteString("  - {id: i" + strconv.Itoa(i) + ", label: x}\n")
	}
	if p, err := Load(fsWith(map[string]*string{"rooms/r.yaml": s(many.String())})); err != nil || len(p.Rooms["r"].Items) != MaxItems {
		t.Errorf("%d items: %v", MaxItems, err)
	}
}

func TestRoomItemErrors(t *testing.T) {
	item := func(id, label string) string {
		return goodRoom + "items:\n  - id: " + id + "\n    label: " + label + "\n"
	}
	var many strings.Builder
	many.WriteString(goodRoom + "items:\n")
	for i := range MaxItems + 1 {
		many.WriteString("  - {id: i" + strconv.Itoa(i) + ", label: x}\n")
	}
	tests := []struct {
		name string
		room string
		want string
	}{
		{"too many", many.String(), "21 items, at most 20"},
		{"duplicate id", goodRoom + "items:\n  - {id: a, label: x}\n  - {id: a, label: y}\n", `id "a"`},
		{"upper case id", item("Straw", "x"), `id "Straw"`},
		{"empty id", item(`""`, "x"), `id ""`},
		{"missing id", goodRoom + "items:\n  - label: x\n", `id ""`},
		{"id with slash", item("a/b", "x"), "id"},
		{"id 65 bytes", item(strings.Repeat("a", 65), "x"), "id"},
		{"missing label", goodRoom + "items:\n  - id: a\n", `label of "a"`},
		{"blank label", item("a", `"  "`), "label"},
		{"two-line label", item("a", `"a\nb"`), "label"},
		{"carriage return", item("a", `"a\rb"`), "label"},
		{"label 101 bytes", item("a", strings.Repeat("x", MaxLabelBytes+1)), "label"},
		{"unknown field", goodRoom + "items:\n  - {id: a, label: x, icon: y}\n", "icon"},
		{"not a list", goodRoom + "items: straw\n", "rooms/r.yaml"},
		{"list of strings", goodRoom + "items: [straw]\n", "rooms/r.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(fsWith(map[string]*string{"rooms/r.yaml": s(tt.room)}))
			if err == nil {
				t.Fatal("Load = nil error")
			}
			if !strings.Contains(err.Error(), "rooms/r.yaml") || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q, want rooms/r.yaml and %q", err, tt.want)
			}
		})
	}
}

// assetServer serves pack t with a.png and pack u with b.gif.
func assetServer(t *testing.T) (*httptest.Server, *Pack) {
	t.Helper()
	p, err := Load(packFS(map[string]*fstest.MapFile{"assets/a.png": file(image(".png", 300))}))
	if err != nil {
		t.Fatal(err)
	}
	u, err := Load(packFS(map[string]*fstest.MapFile{
		"pack.yaml":    file([]byte("id: u\nversion: 1.0.0\ntitle: U\nstart: r\n")),
		"assets/b.gif": file(image(".gif", 50)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(AssetHandler(p, u))
	t.Cleanup(hs.Close)
	return hs, p
}

func do(t *testing.T, method, url string, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	// No redirects: a cleaned path must not lead anywhere either.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestAssetHandlerServes(t *testing.T) {
	hs, p := assetServer(t)
	resp, body := do(t, "GET", hs.URL+p.AssetURL("a.png"), nil)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, p.Assets["a.png"].Data) {
		t.Fatalf("GET = %d, %d bytes", resp.StatusCode, len(body))
	}
	for k, want := range map[string]string{
		"Content-Type":            "image/png",
		"Content-Length":          "300",
		"Cache-Control":           "public, max-age=300",
		"Content-Security-Policy": "default-src 'none'; sandbox",
		"ETag":                    p.Assets["a.png"].etag,
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if resp.Header.Get("Last-Modified") != "" {
		t.Errorf("Last-Modified = %q, want none (assets have no time)", resp.Header.Get("Last-Modified"))
	}
	// The second pack is served by the same handler.
	if resp, body := do(t, "GET", hs.URL+"/packs/u/1.0.0/assets/b.gif", nil); resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/gif" || len(body) != 50 {
		t.Errorf("pack u: %d %q, %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	}
}

func TestAssetHandlerConditionalAndHead(t *testing.T) {
	hs, p := assetServer(t)
	url := hs.URL + p.AssetURL("a.png")
	etag := p.Assets["a.png"].etag
	tests := []struct {
		name   string
		method string
		header map[string]string
		status int
		body   int
	}{
		{"etag matches", "GET", map[string]string{"If-None-Match": etag}, http.StatusNotModified, 0},
		{"etag in a list", "GET", map[string]string{"If-None-Match": `"other", ` + etag}, http.StatusNotModified, 0},
		{"any etag", "GET", map[string]string{"If-None-Match": "*"}, http.StatusNotModified, 0},
		{"weak etag matches", "GET", map[string]string{"If-None-Match": "W/" + etag}, http.StatusNotModified, 0},
		{"other etag", "GET", map[string]string{"If-None-Match": `"0123"`}, http.StatusOK, 300},
		{"head", "HEAD", nil, http.StatusOK, 0},
		{"head with etag", "HEAD", map[string]string{"If-None-Match": etag}, http.StatusNotModified, 0},
		{"range", "GET", map[string]string{"Range": "bytes=0-7"}, http.StatusPartialContent, 8},
		{"range past the end", "GET", map[string]string{"Range": "bytes=1000-"}, http.StatusRequestedRangeNotSatisfiable, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := do(t, tt.method, url, tt.header)
			if resp.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if tt.body >= 0 && len(body) != tt.body {
				t.Errorf("body = %d bytes, want %d", len(body), tt.body)
			}
			// http.Error drops the ETag on a 416; every other answer has it.
			if resp.StatusCode < 400 && resp.Header.Get("ETag") != etag {
				t.Errorf("ETag = %q", resp.Header.Get("ETag"))
			}
		})
	}
	resp, _ := do(t, "HEAD", url, nil)
	if resp.Header.Get("Content-Length") != "300" || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("HEAD headers = %v", resp.Header)
	}
}

func TestAssetHandlerNotFound(t *testing.T) {
	hs, _ := assetServer(t)
	tests := []struct {
		path   string
		status int
	}{
		{"/packs/nope/1.0.0/assets/a.png", http.StatusNotFound},
		{"/packs/t/1.0.0/assets/missing.png", http.StatusNotFound},
		{"/packs/t/1.0.0/assets/b.gif", http.StatusNotFound}, // pack u's image
		{"/packs/u/1.0.0/assets/a.png", http.StatusNotFound},
		{"/packs/t/1.0.0/assets/A.PNG", http.StatusNotFound},
		{"/packs/t/1.0.0/assets/", http.StatusNotFound},
		{"/packs/t/1.0.0/assets/a.png/x", http.StatusNotFound},
		{"/packs/t/1.0.0/assets/%2e%2e%2fpack.yaml", http.StatusNotFound},
		{"/packs/t/1.0.0/assets/..%2fpack.yaml", http.StatusNotFound},
		{"/packs/t/1.0.0/assets/a.png%00", http.StatusNotFound},
		{"/packs/t/pack.yaml", http.StatusNotFound},
		{"/packs/t/rooms/r.yaml", http.StatusNotFound},
		{"/packs/", http.StatusNotFound},
		{"/packs/%74/1.0.0/assets/a.png", http.StatusOK}, // %74 is "t": the same file
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, body := do(t, "GET", hs.URL+tt.path, nil)
			if resp.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if tt.status != http.StatusOK && (bytes.Contains(body, []byte("PNG")) || bytes.Contains(body, []byte("title:"))) {
				t.Errorf("body leaks a file: %q", body)
			}
		})
	}
	// A path with dot segments is cleaned by a redirect, which leads to a
	// 404, never to a file outside assets/.
	resp, _ := do(t, "GET", hs.URL+"/packs/t/1.0.0/assets/../pack.yaml", nil)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("dot segments: status %d", resp.StatusCode)
	}
}

func TestAssetHandlerMethods(t *testing.T) {
	hs, p := assetServer(t)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if resp, _ := do(t, method, hs.URL+p.AssetURL("a.png"), nil); resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", method, resp.StatusCode)
		}
	}
}

func TestAssetHandlerWithoutPacks(t *testing.T) {
	hs := httptest.NewServer(AssetHandler())
	defer hs.Close()
	if resp, _ := do(t, "GET", hs.URL+"/packs/t/1.0.0/assets/a.png", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// The ETag follows the content: the same bytes give the same tag after a
// restart, other bytes another one.
func TestAssetETag(t *testing.T) {
	load := func(data []byte) string {
		t.Helper()
		p, err := Load(packFS(map[string]*fstest.MapFile{"assets/a.png": file(data)}))
		if err != nil {
			t.Fatal(err)
		}
		return p.Assets["a.png"].etag
	}
	a, again, other := load(image(".png", 64)), load(image(".png", 64)), load(image(".png", 65))
	if a != again {
		t.Errorf("same bytes, tags %s and %s", a, again)
	}
	if a == other {
		t.Errorf("different bytes, same tag %s", a)
	}
	if len(a) != 34 || a[0] != '"' || a[33] != '"' {
		t.Errorf("tag %s is not a quoted 32-digit hash", a)
	}
}

// The shipped packs' images all load and are what their names say.
func TestShippedTestPackAssets(t *testing.T) {
	p, err := Load(os.DirFS("../../content/test-pack"))
	if err != nil {
		t.Fatal(err)
	}
	if a := p.Assets["square.png"]; a == nil || a.Type != "image/png" {
		t.Errorf("square.png = %+v", a)
	}
}
