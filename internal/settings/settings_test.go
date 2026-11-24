package settings

import (
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x", "settings.json")
	s := Load(p)
	if !s.Descending {
		t.Error("default order is newest first")
	}
	for _, r := range []string{"a", "b", "c", "d", "e", "f", "g", "b"} {
		s.Opened(r, "")
	}
	s.ListView = true
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := Load(p)
	if s2.Library != "b" || len(s2.Recent) != MaxRecent || s2.Recent[0] != "b" || s2.Recent[1] != "g" || !s2.ListView {
		t.Errorf("loaded %+v", s2)
	}
}
