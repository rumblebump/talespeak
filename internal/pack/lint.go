package pack

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/gopher-lua/parse"
	"go.yaml.in/yaml/v3"
)

// A pack works offline: nothing in it may point to another server. Lint
// checks the text a pack's players can see or a browser could load, which
// is every YAML string and every Lua string literal; comments are not
// checked, so credits and links for authors can go there or in a README,
// which the engine never reads.

// externalRef matches a URL with a scheme (https://, ws://, ftp://), a
// //host reference (a protocol-relative URL, also inside CSS url()), and a
// bare www. host.
var externalRef = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://|(^|[^:/a-z0-9])//[a-z0-9\[]|\bwww\.[a-z0-9-]+\.[a-z]`)

// maxLintErrors bounds the errors one file reports.
const maxLintErrors = 20

// lintYAML reports every string in a YAML document, keys included, that
// holds an external reference, with its line.
func lintYAML(file string, data []byte) []error {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil // decode reports the syntax error
	}
	var errs []error
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if len(errs) >= maxLintErrors {
			return
		}
		if n.Kind == yaml.ScalarNode && externalRef.MatchString(n.Value) {
			errs = append(errs, refError(file, n.Line, n.Value))
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&doc)
	return errs
}

// lintLua reports every string literal of a Lua script that holds an
// external reference, with its line. A script that does not scan is left
// to the compiler, which reports it when the world starts.
func lintLua(file, src string) []error {
	sc := parse.NewScanner(strings.NewReader(src), file)
	lx := &parse.Lexer{}
	var errs []error
	for len(errs) < maxLintErrors {
		tok, err := sc.Scan(lx)
		if err != nil || tok.Type < 0 {
			break
		}
		lx.PrevTokenType = tok.Type
		if tok.Type == parse.TString && externalRef.MatchString(tok.Str) {
			errs = append(errs, refError(file, tok.Pos.Line, tok.Str))
		}
	}
	return errs
}

// refError names the reference found in s, cut to a readable length.
func refError(file string, line int, s string) error {
	loc := externalRef.FindStringIndex(s)
	start := max(0, loc[0]-10)
	end := min(len(s), loc[1]+30)
	snippet := strings.ToValidUTF8(s[start:end], "?")
	return fmt.Errorf("%s:%d: external reference %q: a pack must work offline, so it carries its own files and links nowhere", file, line, snippet)
}

// webFiles are file types a browser would run or load more from. They are
// not part of the pack format yet; a pack that carries one is refused, so
// an author never ships a page or style the engine silently skips. When
// they are added, lint must cover their url(), src and href.
var webFiles = map[string]bool{".css": true, ".svg": true, ".html": true, ".htm": true, ".xhtml": true, ".js": true, ".mjs": true, ".wasm": true}

// maxPackFiles bounds the walk over a pack's files.
const maxPackFiles = 5000

// lintFiles refuses web files anywhere in the pack.
func lintFiles(fsys fs.FS) []error {
	var errs []error
	n := 0
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if n++; n > maxPackFiles {
			return fmt.Errorf("more than %d files", maxPackFiles)
		}
		if !d.IsDir() && webFiles[strings.ToLower(path.Ext(name))] && len(errs) < maxLintErrors {
			errs = append(errs, fmt.Errorf("%s: %s files are not supported in a pack", name, strings.ToLower(path.Ext(name))))
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("pack files: %w", err))
	}
	return errs
}
