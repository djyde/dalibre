package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/opds"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// OPDS in Dalibre (Figma "08 · OPDS & Settings"): catalogs the user adds
// show in the sidebar, where each opens a page browsing its feeds; ones
// that can search are Get books sources too. They are book sources written
// in Go (plugin.NewNative), so they reach the network through the same
// sandboxed client as plugins, and their books download like any source's.
// And the open library can be shared on the network as a catalog.

// catalogPrefix starts the plugin id of a catalog; plugin ids can't hold
// a colon, so the two never clash.
const catalogPrefix = "opds:"

// secretStore keeps passwords; the system's credential store in the app,
// a map in tests.
type secretStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

type systemSecrets struct{}

func (systemSecrets) Get(key string) (string, error) {
	b, err := mygo.Secrets.Get(key)
	return string(b), err
}
func (systemSecrets) Set(key, value string) error { return mygo.Secrets.Set(key, []byte(value)) }
func (systemSecrets) Delete(key string) error     { return mygo.Secrets.Delete(key) }

type memorySecrets map[string]string

func (m memorySecrets) Get(key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (m memorySecrets) Set(key, value string) error { m[key] = value; return nil }
func (m memorySecrets) Delete(key string) error     { delete(m, key); return nil }

func catalogSecret(id string) string { return "opds-catalog-" + id }

const shareSecret = "opds-share"

// catalogStatus is what checking a catalog found.
type catalogStatus struct {
	checking   bool
	err        error
	title      string // the root feed's title
	searchable bool
}

// statusText says how a catalog is, as its row shows it.
func (s *catalogStatus) text() (string, string) {
	switch {
	case s == nil || s.checking:
		return locales.OpdsChecking(), "muted"
	case s.err == nil:
		return locales.OpdsConnected(), "success"
	case errKind(s.err) == plugin.KindAuth:
		return locales.OpdsNeedsSignIn(), "warning"
	}
	return locales.OpdsCantReach(), "danger"
}

func errKind(err error) plugin.ErrorKind {
	var pe *plugin.Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return ""
}

// newCatalogSource makes the book source of a catalog.
func newCatalogSource(cat *settings.Catalog, password string, transport http.RoundTripper) *plugin.Plugin {
	host := ""
	if u, err := url.Parse(cat.URL); err == nil {
		host = u.Host
	}
	m := &plugin.Manifest{
		ID: catalogPrefix + cat.ID, Name: cat.Name, Version: "1.0.0", API: plugin.APIVersion,
		Description:  locales.OpdsCatalogDescription(host),
		Capabilities: plugin.Capabilities{BookSource: &plugin.BookSource{}},
		// A catalog's files and covers may be anywhere it links to; it
		// reaches the local network only when the user said it is there.
		Permissions: plugin.Permissions{Hosts: []string{"*"}, LocalNetwork: cat.LocalNetwork},
	}
	user := cat.Username
	return plugin.NewNative(m, &opds.Source{Root: cat.URL}, plugin.Options{
		Transport: transport,
		Authorize: func(req *http.Request) {
			// The account is the catalog's: never sent to another host.
			if user != "" && req.URL.Host == host {
				req.SetBasicAuth(user, password)
			}
		},
	})
}

// loadCatalogs makes the catalogs' sources from the settings, reading
// their passwords in the background, and checks each.
func (a *App) loadCatalogs() {
	cats := make([]settings.Catalog, 0, len(a.settings.OPDS.Catalogs))
	for _, c := range a.settings.OPDS.Catalogs {
		cats = append(cats, *c)
	}
	secrets, transport := a.secrets, a.pluginTransport
	a.run(func() {
		var out []*plugin.Plugin
		for i := range cats {
			pw := ""
			if cats[i].Username != "" && secrets != nil {
				pw, _ = secrets.Get(catalogSecret(cats[i].ID))
			}
			out = append(out, newCatalogSource(&cats[i], pw, transport))
		}
		a.post(func() {
			a.catalogs = out
			if a.catalogStatus == nil {
				a.catalogStatus = map[string]*catalogStatus{}
			}
			for _, p := range out {
				a.checkCatalog(p)
			}
			if a.page == pageCatalog && a.catalogByID(a.cp.id) == nil {
				a.page = pageLibrary
			}
			// Get books keeps no results of the old sources.
			a.setPlugins(a.plugins)
		})
	})
}

// checkCatalog reads a catalog's root feed: whether it answers, its
// title, and whether it can search.
func (a *App) checkCatalog(p *plugin.Plugin) {
	id := strings.TrimPrefix(p.Manifest.ID, catalogPrefix)
	st := &catalogStatus{checking: true}
	a.catalogStatus[id] = st
	src := p.Native().(*opds.Source)
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		f, err := opds.Fetch(ctx, p.Fetcher(), src.Root)
		searchable := false
		if err == nil {
			t, _ := src.SearchTemplate(ctx, p.Fetcher())
			searchable = t != ""
		}
		a.post(func() {
			st.checking, st.err, st.searchable = false, err, searchable
			if f != nil {
				st.title = f.Title
			}
		})
	})
}

// catalogByID is the source of catalog id (its settings id).
func (a *App) catalogByID(id string) *plugin.Plugin {
	for _, p := range a.catalogs {
		if p.Manifest.ID == catalogPrefix+id {
			return p
		}
	}
	return nil
}

// newCatalogID makes an id for a new catalog.
func newCatalogID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// saveCatalog adds or changes a catalog, keeping its password in the
// credential store.
func (a *App) saveCatalog(cat settings.Catalog, password string) {
	if cat.ID == "" {
		cat.ID = newCatalogID()
		a.settings.OPDS.Catalogs = append(a.settings.OPDS.Catalogs, &cat)
	} else if old := a.settings.OPDS.Catalog(cat.ID); old != nil {
		*old = cat
	}
	a.saveSettings()
	secrets, id := a.secrets, cat.ID
	a.run(func() {
		var err error
		if secrets != nil {
			if cat.Username != "" && password != "" {
				err = secrets.Set(catalogSecret(id), password)
			} else if cat.Username == "" {
				secrets.Delete(catalogSecret(id))
			}
		}
		a.post(func() {
			if err != nil {
				a.toastError(errors.New(locales.OpdsKeychainError(err.Error())))
			}
			a.loadCatalogs()
		})
	})
}

// removeCatalog forgets catalog id and its password.
func (a *App) removeCatalog(id string) {
	cats := a.settings.OPDS.Catalogs
	for i, c := range cats {
		if c.ID == id {
			a.settings.OPDS.Catalogs = append(cats[:i:i], cats[i+1:]...)
		}
	}
	a.saveSettings()
	if a.secrets != nil {
		secrets := a.secrets
		a.run(func() { secrets.Delete(catalogSecret(id)) })
	}
	delete(a.catalogStatus, id)
	a.loadCatalogs()
}

// ---- Probing an address before adding it

// catalogProbe is what checking an address in the Add catalog sheet
// found.
type catalogProbe struct {
	url        string
	checking   bool
	err        error
	title      string
	entries    int
	searchable bool
	local      bool
}

// probeCatalog reads the feed at rawURL as a catalog would, with the
// account given, to say what it is before it is added.
func (a *App) probeCatalog(rawURL, user, password string) *catalogProbe {
	pr := &catalogProbe{url: rawURL, checking: true}
	transport := a.pluginTransport
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		u, err := url.Parse(rawURL)
		if err == nil && (u.Scheme != "http" && u.Scheme != "https" || u.Host == "") {
			err = errors.New(locales.OpdsAddressError())
		}
		var f *opds.Feed
		local, searchable := false, false
		if err == nil {
			local = plugin.IsLocalHost(ctx, u.Hostname())
			p := newCatalogSource(&settings.Catalog{ID: "probe", Name: "probe", URL: rawURL, Username: user, LocalNetwork: local}, password, transport)
			f, err = opds.Fetch(ctx, p.Fetcher(), rawURL)
			if err == nil {
				searchable = opds.SearchTemplate(ctx, p.Fetcher(), f) != ""
			}
		}
		a.post(func() {
			pr.checking, pr.err, pr.local, pr.searchable = false, err, local, searchable
			if f != nil {
				pr.title, pr.entries = f.Title, len(f.Entries)
				if f.Total > 0 {
					pr.entries = f.Total
				}
			}
		})
	})
	return pr
}

// ---- The catalog page

// catalogPage is the page browsing a catalog: the feeds opened from its
// root, as a path the breadcrumb shows.
type catalogPage struct {
	id    string
	path  []crumb
	feeds map[string]*feedState
	text  string // the search box
}

type crumb struct{ title, url string }

// feedState is a feed being read, with the pages after its first.
type feedState struct {
	loading, loadingMore bool
	err                  error
	feed                 *opds.Feed
	entries              []opds.Entry
	next                 string
}

// openCatalog shows catalog id's root feed.
func (a *App) openCatalog(id string) {
	p := a.catalogByID(id)
	if p == nil {
		return
	}
	src := p.Native().(*opds.Source)
	a.page = pageCatalog
	if a.cp.id != id || len(a.cp.path) == 0 {
		a.cp = catalogPage{id: id, feeds: map[string]*feedState{}, path: []crumb{{title: p.Manifest.Name, url: src.Root}}}
	} else {
		a.cp.path = a.cp.path[:1]
	}
	a.cp.text = ""
	a.loadFeed(a.cp.path[0].url, false)
}

// openFeed opens a feed under the one shown.
func (a *App) openFeed(title, u string) {
	a.cp.path = append(a.cp.path, crumb{title: title, url: u})
	a.loadFeed(u, false)
}

// replaceFeed shows another view of the feed shown, as a facet.
func (a *App) replaceFeed(title, u string) {
	if n := len(a.cp.path); n > 0 {
		a.cp.path[n-1] = crumb{title: title, url: u}
	}
	a.loadFeed(u, false)
}

// catalogSearch searches the catalog shown for what the box holds.
func (a *App) catalogSearch() {
	text := strings.TrimSpace(a.cp.text)
	p := a.catalogByID(a.cp.id)
	if text == "" || p == nil {
		return
	}
	src := p.Native().(*opds.Source)
	a.run(func() {
		t, err := src.SearchTemplate(context.Background(), p.Fetcher())
		a.post(func() {
			if err != nil || t == "" {
				a.toast("%s", locales.OpdsCantSearch(p.Manifest.Name))
				return
			}
			a.cp.path = append(a.cp.path[:1], crumb{title: locales.OpdsSearchCrumb(text), url: opds.FillTemplate(t, text)})
			a.loadFeed(a.cp.path[1].url, false)
		})
	})
}

// curFeed is the feed the page shows.
func (a *App) curFeed() *feedState {
	if len(a.cp.path) == 0 {
		return nil
	}
	return a.cp.feeds[a.cp.path[len(a.cp.path)-1].url]
}

// loadFeed reads the feed at u, unless it is read; more reads its next
// page.
func (a *App) loadFeed(u string, more bool) {
	p := a.catalogByID(a.cp.id)
	if p == nil {
		return
	}
	fs := a.cp.feeds[u]
	if fs == nil {
		fs = &feedState{}
		a.cp.feeds[u] = fs
	}
	target := u
	switch {
	case more:
		if fs.loadingMore || fs.next == "" {
			return
		}
		fs.loadingMore, target = true, fs.next
	case fs.loading || fs.feed != nil:
		return
	default:
		fs.loading, fs.err = true, nil
	}
	a.run(func() {
		f, err := opds.Fetch(context.Background(), p.Fetcher(), target)
		a.post(func() {
			fs.loading, fs.loadingMore = false, false
			if err != nil {
				fs.err = err
				return
			}
			if fs.feed == nil {
				fs.feed = f
			}
			fs.entries = append(fs.entries, f.Entries...)
			fs.next = f.Next()
		})
	})
}

// retryFeed reads the feed shown again.
func (a *App) retryFeed() {
	if len(a.cp.path) == 0 {
		return
	}
	u := a.cp.path[len(a.cp.path)-1].url
	delete(a.cp.feeds, u)
	a.loadFeed(u, false)
}

// ---- Sharing the library

// shareState is the server sharing the library, while it runs.
type shareState struct {
	server *http.Server
	opds   *opds.Server
	port   int
	user   string
	err    error
}

// applyShare starts, stops or restarts sharing to match the settings.
func (a *App) applyShare() {
	sh := a.settings.OPDS.Share
	want := sh.On
	port := sh.SharePort()
	user := ""
	if sh.Password {
		user = sh.Username
	}
	if cur := a.share; cur != nil && cur.server != nil {
		if want && cur.port == port && cur.user == user && cur.err == nil {
			return
		}
		srv := cur.server
		go srv.Close()
	}
	a.share = nil
	if !want {
		return
	}
	st := &shareState{port: port, user: user}
	a.share = st
	secrets := a.secrets
	a.run(func() {
		pw := ""
		if user != "" && secrets != nil {
			pw, _ = secrets.Get(shareSecret)
		}
		o := &opds.Server{Library: a.sharedLibrary, Title: "Dalibre", Username: user, Password: pw}
		if user != "" && pw == "" {
			// A username with no password would let anyone in; refuse all.
			o.Password = newCatalogID() + newCatalogID()
		}
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		srv := &http.Server{Handler: o, ReadHeaderTimeout: 10 * time.Second}
		a.post(func() {
			if a.share != st {
				if ln != nil {
					ln.Close()
				}
				return
			}
			if err != nil {
				st.err = errors.New(locales.OpdsSharePortError(strconv.Itoa(port), err.Error()))
				return
			}
			st.server, st.opds = srv, o
			go srv.Serve(ln)
		})
	})
}

// sharedLibrary is the library the server shares; it may run on any
// goroutine.
func (a *App) sharedLibrary() *calibre.Library { return a.libShared.Load() }

// shareAddress is the address readers add, on this computer's network.
func (a *App) shareAddress() string {
	if a.share == nil || a.share.server == nil {
		return ""
	}
	return fmt.Sprintf("http://%s:%d/opds", lanAddress(), a.share.port)
}

// lanAddress is this computer's address on the local network.
func lanAddress() string {
	// Ask the OS routing table which interface holds the default outbound
	// route first, so virtual adapters (such as WSL2/Hyper-V/Docker) don't
	// mask the physical LAN IP before falling back to interface scanning.
	if conn, err := net.Dial("udp4", "198.51.100.1:80"); err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			if ip := addr.IP.To4(); ip != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "localhost"
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok {
				if ip := ipn.IP.To4(); ip != nil && ip.IsPrivate() {
					return ip.String()
				}
			}
		}
	}
	return "localhost"
}
