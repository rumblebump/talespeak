package pack

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Catalog is the packs a server has installed: any number of versions of
// each pack id.
type Catalog struct {
	byID map[string][]*Pack // in preference order: releases newest first, then pre-releases newest first
	ids  []string           // sorted
}

// NewCatalog returns the catalog of packs. Two packs with the same id and
// version are an error.
func NewCatalog(packs ...*Pack) (*Catalog, error) {
	c := &Catalog{byID: map[string][]*Pack{}}
	for _, p := range packs {
		for _, q := range c.byID[p.ID] {
			if q.V.Compare(p.V) == 0 {
				return nil, fmt.Errorf("two packs are %s", p.Name())
			}
		}
		if c.byID[p.ID] == nil {
			c.ids = append(c.ids, p.ID)
		}
		c.byID[p.ID] = append(c.byID[p.ID], p)
	}
	for _, list := range c.byID {
		slices.SortFunc(list, func(a, b *Pack) int {
			if (a.V.Pre == "") != (b.V.Pre == "") {
				if a.V.Pre == "" {
					return -1
				}
				return 1
			}
			return b.V.Compare(a.V)
		})
	}
	slices.Sort(c.ids)
	return c, nil
}

// IDs are the installed pack ids, sorted.
func (c *Catalog) IDs() []string { return slices.Clone(c.ids) }

// Versions are the installed versions of pack id: the releases newest
// first, then the pre-releases newest first.
func (c *Catalog) Versions(id string) []*Pack { return slices.Clone(c.byID[id]) }

// All is every installed pack, by id and then as Versions orders them.
func (c *Catalog) All() []*Pack {
	var all []*Pack
	for _, id := range c.ids {
		all = append(all, c.byID[id]...)
	}
	return all
}

// Newest is the version of pack id new worlds start on: the newest
// release, or the newest pre-release when there is no release; nil when
// none is installed.
func (c *Catalog) Newest(id string) *Pack {
	if list := c.byID[id]; len(list) > 0 {
		return list[0]
	}
	return nil
}

// ErrNotInstalled is the error of Pick for a pack id with no version
// installed.
var ErrNotInstalled = errors.New("not installed")

// NoVersionError is the error of Pick when versions of the pack are
// installed, but none can run the world.
type NoVersionError struct {
	ID        string
	Need      Range
	Installed []string // as Versions orders them
}

func (e *NoVersionError) Error() string {
	return fmt.Sprintf("pack %s needs version %s, and this server has %s", e.ID, e.Need, strings.Join(e.Installed, ", "))
}

// Pick returns the version of pack id a world recorded at version ran
// before should run now: the newest installed version compatible with it
// (see Compatible), so updates within a major version reach the world;
// a release before a pre-release, as Match does.
// An empty recorded version, from a world made before packs had versions,
// takes the newest version (Newest). An unparsable one runs on none.
func (c *Catalog) Pick(id, recorded string) (*Pack, error) {
	list := c.byID[id]
	if len(list) == 0 {
		return nil, fmt.Errorf("pack %s: %w", id, ErrNotInstalled)
	}
	if recorded == "" {
		return list[0], nil
	}
	v, err := ParseVersion(recorded)
	if err != nil {
		return nil, &NoVersionError{ID: id, Need: Range{text: recorded}, Installed: versions(list)}
	}
	return c.Match(id, Compatible(v))
}

// Match returns the newest installed release of pack id in r, or the
// newest pre-release in r when no release is.
func (c *Catalog) Match(id string, r Range) (*Pack, error) {
	list := c.byID[id]
	if len(list) == 0 {
		return nil, fmt.Errorf("pack %s: %w", id, ErrNotInstalled)
	}
	for _, p := range list {
		if r.Contains(p.V) {
			return p, nil
		}
	}
	return nil, &NoVersionError{ID: id, Need: r, Installed: versions(list)}
}

func versions(list []*Pack) []string {
	var vs []string
	for _, p := range list {
		vs = append(vs, p.Version)
	}
	return vs
}
