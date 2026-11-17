package sdk

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".git"), 0o755) // a fresh repository is empty
	files, err := Init(dir, "com.example.books", "Example Books")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plugin.json", "main.js", "dalibre.d.ts", "plugin.schema.json", ".gitignore", ".github/workflows/plugin.yml", "README.md"} {
		if !slices.Contains(files, want) {
			t.Errorf("no %s in %v", want, files)
		}
	}
	wf, _ := os.ReadFile(filepath.Join(dir, ".github", "workflows", "plugin.yml"))
	if !strings.Contains(string(wf), "${{ github.token }}") {
		t.Error("the workflow's expressions were replaced")
	}
	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	if !strings.HasPrefix(string(readme), "# Example Books\n") || strings.Contains(string(readme), "{{") {
		t.Errorf("README not filled in:\n%s", readme)
	}
	if _, err := Init(dir, "com.example.books", "x"); err == nil {
		t.Error("init over a plugin")
	}
}
