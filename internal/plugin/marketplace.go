package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// The Marketplace lists plugins anyone can install: the ones in Dalibre's
// repository (marketplace/), and the community's, which its
// marketplace/community.json points to. CI packs the former and writes
// marketplace.json, the index Dalibre reads, to the repository's
// marketplace branch: each plugin's manifest details and the package to
// download for its listed version.

// MarketplaceURL is where Dalibre reads the Marketplace's index.
const MarketplaceURL = "https://raw.githubusercontent.com/djyde/dalibre/refs/heads/marketplace/marketplace.json"

// Marketplace is the Marketplace's index, marketplace.json.
type Marketplace struct {
	Name       string     `json:"name"`
	Categories []Category `json:"categories"`
	Plugins    []Listing  `json:"plugins"`
}

// Category is a group of plugins the Marketplace filters by.
type Category struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Listing is a plugin in the Marketplace.
type Listing struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	License     string `json:"license,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	Category    string `json:"category,omitempty"`
	// Capabilities are its manifest's: what it adds to Dalibre.
	Capabilities Capabilities `json:"capabilities"`
	// Calibre names the calibre plugin it does the work of, for calibre's
	// users to find it by; Downloads is that plugin's download count.
	// Dalibre is the oldest Dalibre it works in, as its manifest says.
	Dalibre   string `json:"dalibre,omitempty"`
	Calibre   string `json:"calibre,omitempty"`
	Downloads int    `json:"downloads,omitempty"`
	// Repo is the GitHub repository it is developed in, owner/name;
	// Community is true for a plugin from outside the Marketplace's own
	// repository.
	Repo      string `json:"repo"`
	Community bool   `json:"community,omitempty"`
	// Package is where its listed version downloads: a .dalibre-plugin,
	// or a zip of its repository as tagged.
	Package     string      `json:"package"`
	Permissions Permissions `json:"permissions"`
}

// Manifest is the listing as a manifest, enough to show its icon and
// what it asks for; the package's own plugin.json is what installs.
func (l *Listing) Manifest() *Manifest {
	return &Manifest{ID: l.ID, Name: l.Name, Version: l.Version, Description: l.Description,
		Author: l.Author, License: l.License, Icon: l.Icon, Homepage: l.Homepage, Dalibre: l.Dalibre, Capabilities: l.Capabilities, Permissions: l.Permissions}
}

// Find is the listing of plugin id, or nil.
func (m *Marketplace) Find(id string) *Listing {
	if m == nil {
		return nil
	}
	for i := range m.Plugins {
		if m.Plugins[i].ID == id {
			return &m.Plugins[i]
		}
	}
	return nil
}

// ParseMarketplace reads a marketplace.json, leaving out listings that
// can't be installed.
func ParseMarketplace(data []byte) (*Marketplace, error) {
	var m Marketplace
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("the Marketplace’s index can’t be read: %w", err)
	}
	ok := m.Plugins[:0]
	for _, l := range m.Plugins {
		if l.ID == "" || l.Name == "" || Reserved(l.ID) || !semverPattern.MatchString(l.Version) ||
			!packageURL(l.Package) {
			continue
		}
		ok = append(ok, l)
	}
	m.Plugins = ok
	return &m, nil
}

// packageURL reports whether a listing's package may be downloaded from
// u: over https, or from this computer (a test's server).
func packageURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://127.0.0.1:")
}

// FetchMarketplace reads the index at url (MarketplaceURL when empty).
// It returns the raw JSON too, for keeping a copy.
func (g *GitHub) FetchMarketplace(ctx context.Context, url string) (*Marketplace, []byte, error) {
	if url == "" {
		url = MarketplaceURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn’t reach the Marketplace: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, nil, fmt.Errorf("the Marketplace answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, err
	}
	m, err := ParseMarketplace(data)
	return m, data, err
}

// NewerVersion reports whether semantic version a is newer than b. A
// version that can't be read is never newer.
func NewerVersion(a, b string) bool {
	pa, ok := parseVersion(a)
	if !ok {
		return false
	}
	pb, ok := parseVersion(b)
	if !ok {
		return true
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] > pb.n[i]
		}
	}
	// 1.0.0 is newer than 1.0.0-beta.
	return pa.pre == "" && pb.pre != "" || pa.pre != "" && pb.pre != "" && pa.pre > pb.pre
}

type version struct {
	n   [3]int
	pre string
}

func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if !semverPattern.MatchString(s) {
		return version{}, false
	}
	s, _, _ = strings.Cut(s, "+")
	core, pre, _ := strings.Cut(s, "-")
	var v version
	v.pre = pre
	for i, p := range strings.SplitN(core, ".", 3) {
		n, err := strconv.Atoi(p)
		if err != nil {
			return version{}, false
		}
		v.n[i] = n
	}
	return v, true
}

// ErrNotListed is what installing a plugin the Marketplace no longer
// lists fails with.
var ErrNotListed = errors.New("the Marketplace no longer lists this plugin")
