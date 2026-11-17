// Package sdk holds what plugin authors are given: the API's type
// declarations, the manifest's JSON Schema and a starter plugin.
package sdk

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Files are dalibre.d.ts, plugin.schema.json and template/, with its
// hidden files (.gitignore, .github).
//
//go:embed dalibre.d.ts plugin.schema.json all:template
var Files embed.FS

// Init starts a plugin in dir, which must be empty or not there yet, from
// the template: its plugin.json with id and name, main.js, and the type
// declarations and schema editors check them with. It returns the names
// of the files it wrote.
func Init(dir, id, name string) ([]string, error) {
	// A fresh git repository counts as empty.
	if entries, _ := os.ReadDir(dir); slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() != ".git" }) {
		return nil, fmt.Errorf("%s isn’t empty", dir)
	}
	files := map[string]string{"dalibre.d.ts": "dalibre.d.ts", "plugin.schema.json": "plugin.schema.json"}
	err := fs.WalkDir(Files, "template", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files[strings.TrimPrefix(p, "template/")] = p
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	var names []string
	for dst, src := range files {
		data, err := Files.ReadFile(src)
		if err != nil {
			return nil, err
		}
		text := strings.NewReplacer("{{ID}}", id, "{{NAME}}", name).Replace(string(data))
		path := filepath.Join(dir, filepath.FromSlash(dst))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return nil, err
		}
		names = append(names, dst)
	}
	slices.Sort(names)
	return names, nil
}
