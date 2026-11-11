//go:build windows

package sysproxy

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// system reads Windows' internet settings: ProxyEnable, ProxyServer
// ("host:port", or "http=host:port;https=…;socks=…") and ProxyOverride.
func system() Settings {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return Settings{}
	}
	defer k.Close()
	if on, _, err := k.GetIntegerValue("ProxyEnable"); err != nil || on == 0 {
		return Settings{}
	}
	server, _, _ := k.GetStringValue("ProxyServer")
	override, _, _ := k.GetStringValue("ProxyOverride")
	return parseWindows(server, override)
}

func parseWindows(server, override string) Settings {
	var s Settings
	server = strings.TrimSpace(server)
	if server == "" {
		return s
	}
	url := func(scheme, hp string) string {
		hp = strings.TrimSpace(hp)
		if hp == "" {
			return ""
		}
		if strings.Contains(hp, "://") {
			return hp
		}
		return scheme + "://" + hp
	}
	if !strings.Contains(server, "=") {
		s.HTTP, s.HTTPS = url("http", server), url("http", server)
	} else {
		for part := range strings.SplitSeq(server, ";") {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "http":
				s.HTTP = url("http", v)
			case "https":
				s.HTTPS = url("http", v)
			case "socks":
				s.SOCKS = url("socks5", v)
			}
		}
	}
	if !s.IsZero() {
		s.NoProxy = noProxy(strings.Split(override, ";"))
	}
	return s
}
