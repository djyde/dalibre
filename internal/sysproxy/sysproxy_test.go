package sysproxy

import "testing"

func TestNoProxy(t *testing.T) {
	got := noProxy([]string{"localhost", "*.local", "<local>", "10.0.0.0/8", ""})
	if got != "localhost,.local,10.0.0.0/8" {
		t.Errorf("noProxy = %q", got)
	}
}

func TestConfig(t *testing.T) {
	s := Settings{SOCKS: "socks5://127.0.0.1:1080"}
	c := s.config()
	if c.HTTPProxy != s.SOCKS || c.HTTPSProxy != s.SOCKS {
		t.Errorf("socks alone should proxy both: %+v", c)
	}
	s = Settings{HTTP: "http://127.0.0.1:7890"}
	if c := s.config(); c.HTTPSProxy != s.HTTP {
		t.Errorf("https should use the http proxy: %+v", c)
	}
}
