package plugin

import (
	"net/url"
	"path"
	"strings"

	"dalibre/internal/plugin/pkgmgr"
)

// ToolPlace is where a tool was found: its program, its version when
// known, and where it came from (a Source*).
type ToolPlace struct {
	Path, Version, Source string
}

// ToolCommand is the name of a tool's program, as a package manager
// installs it: its build's file name, else its id.
func ToolCommand(t Tool, id string) string {
	b, ok := t.Build()
	if !ok {
		for _, key := range Platforms {
			if bb, ok := t.Platforms[key]; ok {
				b = bb
				break
			}
		}
	}
	name := b.Bin
	if name == "" {
		if u, err := url.Parse(b.URL); err == nil {
			name = path.Base(u.Path)
		}
	}
	name = strings.TrimSuffix(path.Base(name), ".exe")
	if name == "" || name == "." || name == "/" {
		return id
	}
	return name
}

// FindInManager is tool id as package manager mgr installed it, at a
// version the plugin takes.
func FindInManager(t Tool, id, mgr string) (ToolPlace, bool) {
	pkg := t.Packages.Name(mgr)
	if pkg == "" {
		return ToolPlace{}, false
	}
	f, ok := pkgmgr.Find(mgr, pkg, ToolCommand(t, id))
	if !ok || !t.Accepts(f.Version) {
		return ToolPlace{}, false
	}
	return ToolPlace{Path: f.Path, Version: f.Version, Source: mgr}, true
}

// FoundInManager is the first package manager's install of tool id that
// the plugin takes.
func FoundInManager(t Tool, id string) (ToolPlace, bool) {
	for _, mgr := range t.Packages.Names() {
		if p, ok := FindInManager(t, id, mgr); ok {
			return p, true
		}
	}
	return ToolPlace{}, false
}
