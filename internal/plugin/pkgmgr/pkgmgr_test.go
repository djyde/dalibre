package pkgmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindBrew(t *testing.T) {
	root := t.TempDir()
	old := brewPrefixes
	brewPrefixes = func() []string { return []string{root} }
	defer func() { brewPrefixes = old }()

	install := func(name, version, tap string) {
		dir := filepath.Join(root, "Cellar", name, version)
		os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
		os.WriteFile(filepath.Join(dir, "INSTALL_RECEIPT.json"), []byte(`{"source":{"tap":"`+tap+`"}}`), 0o644)
		os.WriteFile(filepath.Join(dir, "bin", name), []byte("x"), 0o755)
	}
	// homebrew/core's zlib is a library, not heartleo/tap's program.
	install("zlib", "1.3.1", "homebrew/core")
	if _, ok := findBrew("heartleo/tap/zlib", "zlib"); ok {
		t.Error("took homebrew/core's zlib for heartleo/tap/zlib")
	}
	install("pandoc", "3.6.4", "homebrew/core")
	install("pandoc", "3.10_1", "homebrew/core")
	f, ok := findBrew("pandoc", "pandoc")
	if !ok || f.Version != "3.10_1" || f.Path != filepath.Join(root, "Cellar", "pandoc", "3.10_1", "bin", "pandoc") {
		t.Errorf("pandoc: %+v %v", f, ok)
	}
	os.MkdirAll(filepath.Join(root, "opt", "pandoc", "bin"), 0o755)
	os.WriteFile(filepath.Join(root, "opt", "pandoc", "bin", "pandoc"), []byte("x"), 0o755)
	if f, _ := findBrew("pandoc", "pandoc"); f.Path != filepath.Join(root, "opt", "pandoc", "bin", "pandoc") {
		t.Errorf("should run through opt: %s", f.Path)
	}
}

func TestCommand(t *testing.T) {
	if got := Command(Brew, "heartleo/tap/zlib"); len(got) != 3 || got[2] != "heartleo/tap/zlib" {
		t.Errorf("brew: %v", got)
	}
}
