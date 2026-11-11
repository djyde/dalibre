//go:build darwin

package sysproxy

import "testing"

func TestParseScutil(t *testing.T) {
	out := []byte(`<dictionary> {
  ExceptionsList : <array> {
    0 : 127.0.0.0/8
    1 : *.local
  }
  HTTPEnable : 1
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7890
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 0
}`)
	s := parseScutil(out)
	if s.HTTP != "http://127.0.0.1:7890" || s.HTTPS != "http://127.0.0.1:7890" || s.SOCKS != "" {
		t.Errorf("proxies: %+v", s)
	}
	if s.NoProxy != "127.0.0.0/8,.local" {
		t.Errorf("NoProxy = %q", s.NoProxy)
	}
	if !parseScutil([]byte("<dictionary> {\n  HTTPEnable : 0\n}")).IsZero() {
		t.Error("a disabled proxy should be none")
	}
}
