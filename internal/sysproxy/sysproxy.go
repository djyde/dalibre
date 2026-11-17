// Package sysproxy reads the proxy the user set for the whole computer:
// the environment's HTTP_PROXY and HTTPS_PROXY when set, else the
// system's own settings (macOS's network settings, Windows' internet
// settings, GNOME's on Linux), which an app started from the Dock or the
// Start menu doesn't get in its environment.
package sysproxy

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// Settings are a proxy configuration: proxy URLs ("http://127.0.0.1:7890",
// "socks5://…"), "" for none, and the hosts that bypass them, as NO_PROXY
// lists them.
type Settings struct {
	HTTP, HTTPS, SOCKS string
	NoProxy            string
}

// IsZero reports whether no proxy is set.
func (s Settings) IsZero() bool { return s.HTTP == "" && s.HTTPS == "" && s.SOCKS == "" }

var (
	mu      sync.Mutex
	cached  Settings
	readAt  time.Time
	refresh = 30 * time.Second
)

// Get is the computer's proxy, read again at most every 30 seconds.
func Get() Settings {
	mu.Lock()
	defer mu.Unlock()
	if !readAt.IsZero() && time.Since(readAt) < refresh {
		return cached
	}
	cached, readAt = read(), time.Now()
	return cached
}

func read() Settings {
	if s := fromEnv(); !s.IsZero() {
		return s
	}
	return system()
}

func fromEnv() Settings {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := os.Getenv(k); v != "" {
				return v
			}
		}
		return ""
	}
	s := Settings{
		HTTP:    get("HTTP_PROXY", "http_proxy"),
		HTTPS:   get("HTTPS_PROXY", "https_proxy"),
		SOCKS:   get("ALL_PROXY", "all_proxy"),
		NoProxy: get("NO_PROXY", "no_proxy"),
	}
	return s
}

func (s Settings) config() *httpproxy.Config {
	c := &httpproxy.Config{HTTPProxy: s.HTTP, HTTPSProxy: s.HTTPS, NoProxy: s.NoProxy}
	if c.HTTPSProxy == "" {
		c.HTTPSProxy = c.HTTPProxy
	}
	if c.HTTPProxy == "" && c.HTTPSProxy == "" && s.SOCKS != "" {
		c.HTTPProxy, c.HTTPSProxy = s.SOCKS, s.SOCKS
	}
	return c
}

// Proxy is http.Transport's Proxy for the computer's proxy.
func Proxy(req *http.Request) (*url.URL, error) {
	return Get().config().ProxyFunc()(req.URL)
}

// Env is the proxy as environment variables, for a program Dalibre runs:
// HTTP_PROXY, HTTPS_PROXY, ALL_PROXY and NO_PROXY, in both cases, as
// programs differ in which they read.
func Env() []string {
	s := Get()
	c := s.config()
	var out []string
	add := func(k, v string) {
		if v != "" {
			out = append(out, k+"="+v, strings.ToLower(k)+"="+v)
		}
	}
	add("HTTP_PROXY", c.HTTPProxy)
	add("HTTPS_PROXY", c.HTTPSProxy)
	add("ALL_PROXY", s.SOCKS)
	add("NO_PROXY", s.NoProxy)
	return out
}

// IsProxy reports whether addr, a host:port being dialed, is the
// computer's proxy: one the user set, which may be on this computer.
func IsProxy(addr string) bool {
	s := Get()
	for _, p := range []string{s.HTTP, s.HTTPS, s.SOCKS} {
		if p == "" {
			continue
		}
		if !strings.Contains(p, "://") {
			p = "http://" + p
		}
		u, err := url.Parse(p)
		if err != nil || u.Host == "" {
			continue
		}
		host, port := u.Hostname(), u.Port()
		if port == "" {
			port = map[string]string{"https": "443", "socks5": "1080", "socks5h": "1080"}[u.Scheme]
			if port == "" {
				port = "80"
			}
		}
		if net.JoinHostPort(host, port) == addr {
			return true
		}
	}
	return false
}

// noProxy joins exception patterns, as the system lists them, into
// NO_PROXY's form: "*.local" becomes ".local", Windows' "<local>" (names
// without a dot) is left out.
func noProxy(patterns []string) string {
	var out []string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		switch {
		case p == "" || p == "<local>":
			continue
		case strings.HasPrefix(p, "*."):
			p = p[1:]
		case p == "*":
		}
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

// hostPort makes a proxy URL of a host and a port.
func hostPort(scheme, host, port string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if port = strings.TrimSpace(port); port != "" && port != "0" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host
}

// Forget drops what Get read, so the next call reads the settings again:
// after the environment changed.
func Forget() {
	mu.Lock()
	readAt = time.Time{}
	mu.Unlock()
}
