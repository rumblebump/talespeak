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

// externalRef matches, in text cleaned by browserURL:
//   - a URL with a scheme (https://, ws://, git+ssh://), or a web scheme
//     without slashes (https:evil.example), which browsers also follow;
//   - javascript: and vbscript: links;
//   - a //host reference (a protocol-relative URL, also inside CSS url()),
//     where host has a dot, a port, an IPv6 address or is localhost, so
//     story text like "//INCOMING TRANSMISSION//" is not one;
//   - a bare www. host.
var externalRef = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://` +
	`|\b(?:https?|wss?|ftp):/?[a-z0-9\[]` +
	`|\b(?:javascript|vbscript):` +
	`|(^|[^:/a-z0-9])//(?:\[|localhost\b|[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}|[a-z0-9.-]+:\d)` +
	`|\bwww\.[a-z0-9-]+\.[a-z]`)

// URL text is read twice: the way a browser's URL parser reads it (a
// backslash is a slash; tabs and line breaks are dropped), and with line
// breaks as spaces, the way a player reads it.
var (
	browserURL = strings.NewReplacer(`\`, "/", "\t", "", "\r", "", "\n", "")
	spacedText = strings.NewReplacer(`\`, "/", "\t", " ", "\r", " ", "\n", " ")
)

// findRef returns the reference in s with some text around it, and false
// when there is none.
func findRef(s string) (string, bool) {
	for _, r := range []*strings.Replacer{spacedText, browserURL} {
		clean := r.Replace(s)
		if loc := externalRef.FindStringIndex(clean); loc != nil {
			start := max(0, loc[0]-10)
			end := min(len(clean), loc[1]+30)
			return strings.ToValidUTF8(clean[start:end], "?"), true
		}
	}
	return "", false
}

// maxLintErrors bounds the errors one file reports.
const maxLintErrors = 20

// lineOf returns the first of the source lines first to last (counted
// from 1) that holds a reference on its own, and first when none does,
// for example when the reference is broken across lines or written with
// escapes.
func lineOf(lines []string, first, last int) int {
	for i := max(first, 1); i <= last && i <= len(lines); i++ {
		if _, ok := findRef(lines[i-1]); ok {
			return i
		}
	}
	return first
}

// lintYAML reports every string in a YAML document, keys included, that
// holds an external reference, with its line. It refuses explicit tags:
// the pack format needs none, and one like !!binary turns the text lint
// sees into other text.
func lintYAML(file string, data []byte) []error {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return nil // decode reports the syntax error
	}
	lines := strings.Split(string(data), "\n")
	var errs []error
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if len(errs) >= maxLintErrors {
			return
		}
		if n.Style&yaml.TaggedStyle != 0 {
			errs = append(errs, fmt.Errorf("%s:%d: YAML tag %q: tags are not part of the pack format", file, n.Line, strings.ToValidUTF8(n.Tag, "?")))
			return
		}
		if n.Kind == yaml.ScalarNode {
			if _, ok := findRef(n.Value); ok {
				// A scalar spans at most one source line per line break
				// or folded space in its value.
				last := n.Line + strings.Count(n.Value, "\n") + strings.Count(n.Value, " ") + 1
				errs = append(errs, refError(file, lineOf(lines, n.Line, last), n.Value))
			}
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
	var lines []string
	var errs []error
	for len(errs) < maxLintErrors {
		tok, err := sc.Scan(lx)
		if err != nil || tok.Type < 0 {
			break
		}
		lx.PrevTokenType = tok.Type
		if tok.Type != parse.TString {
			continue
		}
		if _, ok := findRef(tok.Str); ok {
			if lines == nil {
				lines = strings.Split(src, "\n")
			}
			errs = append(errs, refError(file, lineOf(lines, tok.Pos.Line, sc.Pos.Line), tok.Str))
		}
	}
	return errs
}

// refError reports the reference in s, found on a line of file.
func refError(file string, line int, s string) error {
	ref, _ := findRef(s)
	return fmt.Errorf("%s:%d: external reference %q: a pack must work offline, so it carries its own files and links nowhere", file, line, ref)
}

// webFiles are file types a browser would run or load more from. They are
// not part of the pack format yet; a pack that carries one is refused, so
// an author never ships a page or style the engine silently skips. When
// they are added, lint must cover their url(), src and href.
var webFiles = map[string]bool{
	".css": true, ".svg": true, ".svgz": true, ".html": true, ".htm": true, ".shtml": true, ".xhtml": true, ".xht": true,
	".xml": true, ".xsl": true, ".js": true, ".mjs": true, ".cjs": true, ".wasm": true,
}

// maxPackFiles bounds the walk over a pack's files.
const maxPackFiles = 5000

// lintFiles refuses web files anywhere in the pack. Hidden folders (.git,
// an editor's settings) are skipped: the engine never reads them.
func lintFiles(fsys fs.FS) []error {
	var errs []error
	n := 0
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && name != "." && strings.HasPrefix(d.Name(), ".") {
			return fs.SkipDir
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
