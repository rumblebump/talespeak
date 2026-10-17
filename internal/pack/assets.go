package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"time"
)

// Asset is one image of a pack, kept in memory.
type Asset struct {
	Name string
	// Type is the media type, checked against the file's content.
	Type string
	Data []byte
	etag string
}

// assetTypes are the image formats a pack may carry. SVG is left out until
// pack lint checks it (T-0014): an SVG file can hold scripts and links.
var assetTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

var assetPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}\.(png|jpe?g|gif|webp)$`)

// loadAssets reads every file in assets/. A pack without the folder has no
// images.
func loadAssets(fsys fs.FS, p *Pack, limits Limits) []error {
	entries, err := fs.ReadDir(fsys, "assets")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []error{fmt.Errorf("assets: %w", err)}
	}
	if len(entries) > MaxAssets {
		return []error{fmt.Errorf("assets: %d files, at most %d", len(entries), MaxAssets)}
	}
	var errs []error
	var total int64
	for _, e := range entries {
		file := "assets/" + e.Name()
		if !e.Type().IsRegular() {
			errs = append(errs, fmt.Errorf("%s: only image files are allowed in assets/, no folders or links", file))
			continue
		}
		if !assetPattern.MatchString(e.Name()) {
			errs = append(errs, fmt.Errorf("%s: name must be 1 to 64 of a-z, 0-9, _ and -, then .png, .jpg, .jpeg, .gif or .webp", file))
			continue
		}
		data, err := readLimited(fsys, file, limits.AssetBytes)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", file, err))
			continue
		}
		total += int64(len(data))
		if total > limits.AssetsBytes {
			return append(errs, fmt.Errorf("assets: more than %d bytes in all", limits.AssetsBytes))
		}
		want := assetTypes[path.Ext(e.Name())]
		if got := http.DetectContentType(data); got != want {
			errs = append(errs, fmt.Errorf("%s: content is %s, not %s", file, got, want))
			continue
		}
		sum := sha256.Sum256(data)
		p.Assets[e.Name()] = &Asset{Name: e.Name(), Type: want, Data: data, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
	}
	return errs
}

// AssetURL is the path the engine serves the image name at.
func (p *Pack) AssetURL(name string) string {
	return "/packs/" + p.ID + "/assets/" + name
}

// AssetHandler serves the images of packs at /packs/<pack id>/assets/<name>.
func AssetHandler(packs ...*Pack) http.Handler {
	byID := map[string]*Pack{}
	for _, p := range packs {
		byID[p.ID] = p
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /packs/{pack}/assets/{name}", func(w http.ResponseWriter, r *http.Request) {
		p := byID[r.PathValue("pack")]
		if p == nil {
			http.NotFound(w, r)
			return
		}
		a := p.Assets[r.PathValue("name")]
		if a == nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", a.Type)
		h.Set("ETag", a.etag)
		h.Set("Cache-Control", "public, max-age=300")
		// An image needs nothing else; this stays strict if a browser ever
		// renders one as a document.
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(a.Data))
	})
	return mux
}

// Image is the url and whether the pack has an image named name.
func (p *Pack) Image(name string) (string, bool) {
	if p.Assets[name] == nil {
		return "", false
	}
	return p.AssetURL(name), true
}
