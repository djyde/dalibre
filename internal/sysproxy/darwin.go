//go:build darwin

package sysproxy

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
)

// system reads macOS's proxy, as scutil --proxy prints it.
func system() Settings {
	out, err := exec.Command("/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return Settings{}
	}
	return parseScutil(out)
}

// parseScutil reads scutil --proxy's dictionary.
func parseScutil(out []byte) Settings {
	keys := map[string]string{}
	var exceptions []string
	inList := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if inList {
			if line == "}" {
				inList = false
				continue
			}
			if _, v, ok := strings.Cut(line, " : "); ok {
				exceptions = append(exceptions, strings.TrimSpace(v))
			}
			continue
		}
		k, v, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "ExceptionsList" {
			inList = true
			continue
		}
		keys[k] = v
	}
	on := func(k string) bool { return keys[k+"Enable"] == "1" }
	var s Settings
	if on("HTTP") {
		s.HTTP = hostPort("http", keys["HTTPProxy"], keys["HTTPPort"])
	}
	if on("HTTPS") {
		s.HTTPS = hostPort("http", keys["HTTPSProxy"], keys["HTTPSPort"])
	}
	if on("SOCKS") {
		s.SOCKS = hostPort("socks5", keys["SOCKSProxy"], keys["SOCKSPort"])
	}
	if !s.IsZero() {
		s.NoProxy = noProxy(exceptions)
	}
	return s
}
