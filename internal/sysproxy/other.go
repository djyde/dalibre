//go:build !darwin && !windows

package sysproxy

import (
	"os/exec"
	"strings"
)

// system reads GNOME's proxy, when it is set to manual. Elsewhere on
// Linux the environment is the proxy, which Get reads first.
func system() Settings {
	gs, err := exec.LookPath("gsettings")
	if err != nil {
		return Settings{}
	}
	get := func(schema, key string) string {
		out, err := exec.Command(gs, "get", schema, key).Output()
		if err != nil {
			return ""
		}
		return strings.Trim(strings.TrimSpace(string(out)), "'")
	}
	if get("org.gnome.system.proxy", "mode") != "manual" {
		return Settings{}
	}
	var s Settings
	hp := func(kind string) (string, string) {
		schema := "org.gnome.system.proxy." + kind
		return get(schema, "host"), get(schema, "port")
	}
	if h, p := hp("http"); h != "" {
		s.HTTP = hostPort("http", h, p)
	}
	if h, p := hp("https"); h != "" {
		s.HTTPS = hostPort("http", h, p)
	}
	if h, p := hp("socks"); h != "" {
		s.SOCKS = hostPort("socks5", h, p)
	}
	if !s.IsZero() {
		list := get("org.gnome.system.proxy", "ignore-hosts")
		list = strings.Trim(list, "[]")
		var hosts []string
		for h := range strings.SplitSeq(list, ",") {
			hosts = append(hosts, strings.Trim(strings.TrimSpace(h), "'"))
		}
		s.NoProxy = noProxy(hosts)
	}
	return s
}
