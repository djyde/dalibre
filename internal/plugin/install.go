package plugin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits on a plugin package.
const (
	MaxPackageSize  = 32 << 20 // uncompressed
	MaxPackageFiles = 1000
)

// PackageExt is the extension of a packaged plugin: a zip of its folder.
const PackageExt = ".dalibre-plugin"

// isDir reports whether path is a folder: a plugin's, unpacked.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Inspect reads the manifest in a plugin package, or a plugin's folder,
// without installing it, so the user can be asked to grant its
// permissions first.
func Inspect(archive string) (*Manifest, error) {
	if isDir(archive) {
		m, err := ReadManifest(archive)
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("the folder has no plugin.json")
		}
		if err == nil && Reserved(m.ID) {
			return nil, fmt.Errorf("plugin.json: id %q is kept for plugins that ship with Dalibre", m.ID)
		}
		return m, err
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	prefix, err := packageRoot(&zr.Reader)
	if err != nil {
		return nil, err
	}
	f, err := zr.Open(prefix + "plugin.json")
	if err != nil {
		return nil, errors.New("the package has no plugin.json")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(data)
	if err == nil && Reserved(m.ID) {
		return nil, fmt.Errorf("plugin.json: id %q is kept for plugins that ship with Dalibre", m.ID)
	}
	return m, err
}

// Install unpacks a plugin package into root/<id>, replacing the version
// there, and returns its manifest. archive may be a plugin's folder: it
// is packed as Pack does (hidden files and node_modules left out) and
// installed as that package, so later edits to the folder need another
// install. Granting its permissions is the caller's business, before or
// after.
func Install(archive, root string) (*Manifest, error) {
	if isDir(archive) {
		tmp, err := os.MkdirTemp("", "dalibre-plugin-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		pkg, err := Pack(archive, tmp)
		if err != nil {
			return nil, err
		}
		return Install(pkg, root)
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	prefix, err := packageRoot(&zr.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(root, ".install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	var total int64
	for _, f := range zr.File {
		name, ok := strings.CutPrefix(f.Name, prefix)
		if !ok || name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		if !fs.ValidPath(name) || strings.Contains(name, `\`) {
			return nil, fmt.Errorf("the package has an unsafe path %q", f.Name)
		}
		if !f.Mode().IsRegular() {
			return nil, fmt.Errorf("%s isn’t a regular file", f.Name)
		}
		dst := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		n, err := unzipFile(f, dst, MaxPackageSize-total)
		if err != nil {
			return nil, err
		}
		total += n
	}
	m, err := ReadManifest(tmp)
	if err != nil {
		return nil, err
	}
	if Reserved(m.ID) {
		return nil, fmt.Errorf("plugin.json: id %q is kept for plugins that ship with Dalibre", m.ID)
	}
	if _, err := os.Stat(filepath.Join(tmp, filepath.FromSlash(m.Main))); err != nil {
		return nil, fmt.Errorf("the package has no %s", m.Main)
	}

	dir := filepath.Join(root, m.ID)
	old := filepath.Join(root, ".old-"+m.ID)
	os.RemoveAll(old)
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, old); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.Rename(old, dir)
		return nil, err
	}
	os.RemoveAll(old)
	return m, nil
}

// packageRoot finds the folder holding plugin.json: the package's root,
// or its one top folder.
func packageRoot(zr *zip.Reader) (string, error) {
	if len(zr.File) > MaxPackageFiles {
		return "", fmt.Errorf("the package has over %d files", MaxPackageFiles)
	}
	var total uint64
	// plugin.json is at the package's root or in its one top folder; in a
	// repository downloaded from GitHub, possibly in that folder's plugin
	// folder.
	roots, nested := map[string]bool{}, map[string]bool{}
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if path.Base(f.Name) != "plugin.json" {
			continue
		}
		dir := strings.TrimSuffix(f.Name, "plugin.json")
		switch n := strings.Count(f.Name, "/"); {
		case n <= 1:
			roots[dir] = true
		case n == 2 && strings.HasSuffix(dir, "/plugin/"):
			nested[dir] = true
		}
	}
	if len(roots) == 0 {
		roots = nested
	}
	switch {
	case total > MaxPackageSize:
		return "", fmt.Errorf("the package is over %d MB unpacked", MaxPackageSize>>20)
	case roots[""]:
		return "", nil
	case len(roots) == 1:
		for r := range roots {
			return r, nil
		}
	}
	return "", errors.New("the package has no plugin.json at its root")
}

func unzipFile(f *zip.File, dst string, budget int64) (int64, error) {
	r, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(w, io.LimitReader(r, budget+1))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > budget {
		err = fmt.Errorf("the package is over %d MB unpacked", MaxPackageSize>>20)
	}
	return n, err
}

// Uninstall removes the plugin id from root.
func Uninstall(root, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%q isn’t a plugin id", id)
	}
	dir := filepath.Join(root, id)
	if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err != nil {
		return fmt.Errorf("%s isn’t installed", id)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.RemoveAll(DataDir(root, id))
}

// Pack zips the plugin in dir as <id>-<version>.dalibre-plugin in out,
// leaving out hidden files, and returns the package's path. The plugin
// must load.
func Pack(dir, out string) (string, error) {
	// A plugin using node:fs loads only with folders of its own: give it
	// throwaway ones.
	data, err := os.MkdirTemp("", "dalibre-pack-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(data)
	p, err := Load(dir, Options{DataDir: data, Logf: func(string, ...any) {}})
	if err != nil {
		return "", err
	}
	defer p.Close()
	m := p.Manifest
	dst := filepath.Join(out, m.ID+"-"+m.Version+PackageExt)
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	var total int64
	files := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || strings.HasSuffix(d.Name(), PackageExt) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if total += info.Size(); total > MaxPackageSize {
			return fmt.Errorf("the plugin is over %d MB", MaxPackageSize>>20)
		}
		if files++; files > MaxPackageFiles {
			return fmt.Errorf("the plugin has over %d files", MaxPackageFiles)
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	})
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}
