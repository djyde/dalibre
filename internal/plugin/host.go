package plugin

import (
	"dalibre/internal/sysproxy"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Limits on what a plugin's script may do.
const (
	// MaxFetchBody is the most a fetch response may hold; files go
	// through the host's download, which streams them to disk.
	MaxFetchBody = 16 << 20
	// MaxRequests is how many fetches one call may make.
	MaxRequests = 50
	// MaxRedirects is how many redirects a request may follow.
	MaxRedirects = 10
)

// UserAgent is sent with every request a plugin makes, followed by the
// plugin's id and version.
const UserAgent = "Dalibre"

// ErrHostNotAllowed is returned for a request the plugin's permissions
// don't allow.
var ErrHostNotAllowed = errors.New("not allowed by the plugin's permissions")

// ErrorKind says what went wrong in a plugin, so the app can say what to
// do about it.
type ErrorKind string

// The kinds of Error. Plugins throw the first five with
// dalibre.error(kind, message); the host gives the others, and fetch
// rejects with permission, timeout or unavailable.
const (
	KindAuth        ErrorKind = "auth"        // credentials missing or refused: open the plugin's settings
	KindNotFound    ErrorKind = "notFound"    // the book or file is gone
	KindRateLimited ErrorKind = "rateLimited" // try again later
	KindUnavailable ErrorKind = "unavailable" // the service is down
	KindInvalid     ErrorKind = "invalid"     // a bad query, or the plugin returned something wrong
	KindSettings    ErrorKind = "settings"    // a required setting is empty
	KindTimeout     ErrorKind = "timeout"     // the call ran out of time
	KindPermission  ErrorKind = "permission"  // a request its permissions don't allow
	KindScript      ErrorKind = "script"      // any other error in the script
	KindCancelled   ErrorKind = "cancelled"   // the user cancelled a form the action asked
)

var thrownKinds = []ErrorKind{KindAuth, KindNotFound, KindRateLimited, KindUnavailable, KindInvalid}

// Error is an error from a plugin.
type Error struct {
	Plugin string
	Call   string // "search", "resolve"…
	Kind   ErrorKind
	Msg    string
	Stack  string // where the script threw, for its author
	Err    error  // the host's cause, if any
}

func (e *Error) Error() string {
	s := e.Plugin
	if e.Call != "" {
		s += "." + e.Call
	}
	return s + ": " + e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// hostInfo is __host_info(): what the prelude puts in dalibre, as JSON.
func (p *Plugin) hostInfo([]any) (any, error) {
	data, err := json.Marshal(map[string]any{
		"api":      APIVersion,
		"plugin":   map[string]string{"id": p.Manifest.ID, "version": p.Manifest.Version},
		"settings": p.settings,
		// what a refused request's error says, to tell it from others
		"notAllowed": ErrHostNotAllowed.Error(),
	})
	return string(data), err
}

// hostLog is __host_log(level, text).
func (p *Plugin) hostLog(args []any) (any, error) {
	if len(args) == 2 {
		p.logf("[%s] %v: %s", p.Manifest.ID, args[0], p.redact(fmt.Sprint(args[1])))
	}
	return nil, nil
}

// hostSettle is __host_settle(json): the outcome of the call in progress,
// which ends the loop running it.
func (p *Plugin) hostSettle(args []any) (any, error) {
	if len(args) == 1 {
		if s, ok := args[0].(string); ok && p.settled == nil {
			p.settled = &s
			p.loop.StopNoWait()
		}
	}
	return nil, nil
}

// redact hides the values of secret settings in what the plugin logs.
func (p *Plugin) redact(s string) string {
	for _, set := range p.Manifest.Settings {
		if v := p.settings[set.Key]; set.Type == "secret" && len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "•••")
		}
	}
	return s
}

// httpClient makes the plugin's requests: to the hosts it may reach,
// following redirects only to those, and dialing only public addresses
// unless it may reach the local network. It keeps cookies as a browser
// does, for this plugin alone and in memory, so a plugin can sign in
// once with a form and stay signed in until it is loaded again.
func (p *Plugin) httpClient(t http.RoundTripper) *http.Client {
	if t == nil {
		t = sandboxTransport(p.Manifest.Permissions.LocalNetwork)
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &http.Client{
		Transport: t,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return errors.New("too many redirects")
			}
			return p.checkURL(req.URL)
		},
	}
}

// SystemTransport is a transport through the computer's proxy, as the
// user set it in the system's settings or the environment.
func SystemTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = sysproxy.Proxy
	return t
}

func sandboxTransport(localNetwork bool) http.RoundTripper {
	t := SystemTransport()
	// The user's proxy, as set for the computer, may be on this computer;
	// it is dialed as is. Every other address is checked after name
	// resolution, so a listed host can't be pointed at the local network.
	open := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	checked := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return err
			}
			if !localNetwork && isLocal(ap.Addr()) {
				return fmt.Errorf("%w: %s is on this computer or the local network", ErrHostNotAllowed, ap.Addr())
			}
			return nil
		}}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if sysproxy.IsProxy(addr) {
			return open.DialContext(ctx, network, addr)
		}
		return checked.DialContext(ctx, network, addr)
	}
	return t
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func isLocal(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() || cgnat.Contains(a)
}

// checkURL refuses what the plugin may not reach.
func (p *Plugin) checkURL(u *url.URL) error {
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("%w: %s isn’t an http or https URL", ErrHostNotAllowed, u.Redacted())
	}
	if u.User != nil {
		return fmt.Errorf("%w: put credentials in headers, not in the URL", ErrHostNotAllowed)
	}
	if !p.Manifest.Permissions.allows(u.Hostname()) {
		return fmt.Errorf("%w: %s isn’t in permissions.hosts", ErrHostNotAllowed, u.Hostname())
	}
	return nil
}

// request builds a request the plugin may make.
func (p *Plugin) request(ctx context.Context, method, rawURL string, headers map[string]string, body io.Reader) (*http.Request, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !u.IsAbs() {
		return nil, fmt.Errorf("%q isn’t an absolute URL", rawURL)
	}
	if err := p.checkURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent+" "+p.Manifest.ID+"/"+p.Manifest.Version)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if p.authorize != nil {
		p.authorize(req)
	}
	return req, nil
}
