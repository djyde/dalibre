package mcp

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"

	plugsdk "dalibre/sdk"
)

// dalibre-plugin dev reaches the running app with its token and adds a
// plugin's folder.
func TestDial(t *testing.T) {
	s := New(&testHost{lib: testLibrary(t)}, "test", true)
	s.SetToken("secret")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "p")
	if _, err := plugsdk.Init(dir, "com.example.dial", "Dial"); err != nil {
		t.Fatal(err)
	}
	cs, err := Dial(ctx, srv.URL+Path, "secret", "dalibre-plugin", "test")
	if err != nil {
		t.Fatal(err)
	}
	var out addPluginOut
	if err := CallTool(ctx, cs, "add_plugin", map[string]string{"path": dir}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Plugin.ID != "com.example.dial" || out.Plugin.State != PluginLoaded {
		t.Errorf("added %+v", out.Plugin)
	}
	if err := CallTool(ctx, cs, "add_plugin", map[string]string{"path": "relative"}, nil); err == nil {
		t.Error("a tool's failure isn't an error")
	}

	if _, err := Dial(ctx, srv.URL+Path, "wrong", "dalibre-plugin", "test"); err == nil {
		t.Error("a wrong token connected")
	}
	cs.Close()
	srv.Close()
	if _, err := Dial(ctx, srv.URL+Path, "secret", "dalibre-plugin", "test"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("nothing listening: %v", err)
	}
}
