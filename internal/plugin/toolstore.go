package plugin

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ToolStore keeps the tool builds Dalibre downloaded, one folder each,
// named by the build's SHA-256, so plugins declaring the same build share
// it.
type ToolStore struct {
	// Dir holds the builds: ToolsDir of the plugins folder.
	Dir string
	// Client downloads them; one with the system's proxy when nil.
	Client *http.Client
}

// ToolsDir is the store's folder for plugins folder root: hidden beside
// the plugins, which Discover skips.
func ToolsDir(root string) string { return filepath.Join(root, ".tools") }

// complete marks a build's folder as finished: unpacked and checked.
const complete = ".complete"

// buildDir is the folder of build b.
func (s *ToolStore) buildDir(b Build) string { return filepath.Join(s.Dir, b.SHA256) }

// Path is where build b's executable is, when the store has it.
func (s *ToolStore) Path(b Build) (string, bool) {
	if s == nil || s.Dir == "" || !sha256Pattern.MatchString(b.SHA256) {
		return "", false
	}
	dir := s.buildDir(b)
	if _, err := os.Stat(filepath.Join(dir, complete)); err != nil {
		return "", false
	}
	exe := filepath.Join(dir, "files", filepath.FromSlash(buildBin(b)))
	if st, err := os.Stat(exe); err != nil || st.IsDir() {
		return "", false
	}
	return exe, true
}

// buildBin is the executable's path in a build's files: Bin, or the
// file's name for a build that is the executable.
func buildBin(b Build) string {
	if b.Bin != "" {
		return b.Bin
	}
	name := "tool"
	if u, err := url.Parse(b.URL); err == nil && path.Base(u.Path) != "" && path.Base(u.Path) != "/" {
		name = path.Base(u.Path)
	}
	return name
}

func (s *ToolStore) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Transport: SystemTransport(), Timeout: 30 * time.Minute}
}

// Fetch downloads build b into the store, unless it has it, and returns
// its executable's path. The download is checked against the build's
// size and SHA-256 before anything is unpacked. progress, when not nil,
// is told the bytes so far and the total (-1 when unknown).
func (s *ToolStore) Fetch(ctx context.Context, b Build, progress func(done, total int64)) (string, error) {
	if exe, ok := s.Path(b); ok {
		return exe, nil
	}
	if !sha256Pattern.MatchString(b.SHA256) {
		return "", errors.New("the build has no SHA-256")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(s.Dir, ".fetch-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	files := filepath.Join(tmp, "files")
	if err := s.Unpack(ctx, b, files, progress); err != nil {
		return "", err
	}
	bin := buildBin(b)
	exe := filepath.Join(files, filepath.FromSlash(bin))
	st, err := os.Stat(exe)
	if err != nil || st.IsDir() {
		return "", fmt.Errorf("the download has no %s", bin)
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, complete), nil, 0o644); err != nil {
		return "", err
	}
	dst := s.buildDir(b)
	os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	exe, ok := s.Path(b)
	if !ok {
		return "", errors.New("the build isn’t where it was put")
	}
	return exe, nil
}

// Unpack downloads build b, checks it is the build named, and unpacks
// it into dir: an archive's files, or the executable named as its URL
// is.
func (s *ToolStore) Unpack(ctx context.Context, b Build, dir string, progress func(done, total int64)) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "download")
	if err := s.download(ctx, b, file, progress); err != nil {
		return err
	}
	return UnpackBuild(file, b, dir)
}

// UnpackBuild unpacks file, build b's download, into dir.
func UnpackBuild(file string, b Build, dir string) error {
	var err error
	switch lower := strings.ToLower(urlPath(b.URL)); {
	case strings.HasSuffix(lower, ".zip"):
		err = unpackZip(file, dir)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		err = unpackTarGz(file, dir)
	default:
		err = os.Rename(file, filepath.Join(dir, buildBin(b)))
	}
	if err != nil {
		return fmt.Errorf("couldn’t unpack it: %w", err)
	}
	return nil
}

func urlPath(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		return u.Path
	}
	return rawURL
}

// download saves b's file as dst, checking its size and hash.
func (s *ToolStore) download(ctx context.Context, b Build, dst string, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("couldn’t download it: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("couldn’t download it: %s answered %s", req.URL.Host, resp.Status)
	}
	total := resp.ContentLength
	if b.Size > 0 {
		total = b.Size
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	h := sha256.New()
	w := &countingWriter{w: io.MultiWriter(out, h), total: total, progress: progress}
	n, err := io.Copy(w, io.LimitReader(resp.Body, MaxToolSize+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("couldn’t download it: %w", err)
	case n > MaxToolSize:
		return fmt.Errorf("the download is over %d MB", MaxToolSize>>20)
	case b.Size > 0 && n != b.Size:
		return fmt.Errorf("the download is %d bytes, not the %d the plugin says", n, b.Size)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != b.SHA256 {
		return &BuildMismatch{URL: b.URL, Want: b.SHA256, Got: sum}
	}
	return nil
}

// BuildMismatch is a download whose hash isn't the build's: changed
// where it is kept, or not the file the plugin's author checked.
type BuildMismatch struct{ URL, Want, Got string }

func (e *BuildMismatch) Error() string {
	return "the download isn’t the build the plugin names (its SHA-256 differs); it wasn’t kept"
}

// Keep removes the builds not in keep (SHA-256s): those no installed
// plugin declares any more.
func (s *ToolStore) Keep(keep map[string]bool) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && sha256Pattern.MatchString(e.Name()) && !keep[e.Name()] {
			os.RemoveAll(filepath.Join(s.Dir, e.Name()))
		}
	}
}

// inside is the path of name, an archive's entry, under dir; false when
// the name would leave it.
func inside(dir, name string) (string, bool) {
	name = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, `\`, "/")), "/")
	if name == "" || name == "." || !fs.ValidPath(name) {
		return "", false
	}
	return filepath.Join(dir, filepath.FromSlash(name)), true
}

// unpackBudget bounds what an archive may unpack to.
const unpackBudget = 2 << 30

func unpackZip(file, dir string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	var total int64
	for _, f := range zr.File {
		dst, ok := inside(dir, f.Name)
		if !ok {
			continue
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		case mode&fs.ModeSymlink != 0:
			r, err := f.Open()
			if err != nil {
				return err
			}
			target, err := io.ReadAll(io.LimitReader(r, 4096))
			r.Close()
			if err != nil {
				return err
			}
			if err := safeSymlink(dir, dst, string(target)); err != nil {
				return err
			}
			continue
		case !mode.IsRegular():
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		n, err := writeEntry(dst, r, mode, unpackBudget-total)
		r.Close()
		if err != nil {
			return err
		}
		total += n
	}
	return nil
}

func unpackTarGz(file, dir string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		dst, ok := inside(dir, h.Name)
		if !ok {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := safeSymlink(dir, dst, h.Linkname); err != nil {
				return err
			}
		case tar.TypeReg:
			n, err := writeEntry(dst, tr, fs.FileMode(h.Mode), unpackBudget-total)
			if err != nil {
				return err
			}
			total += n
		}
	}
}

// writeEntry writes an archive's file, executable when it was.
func writeEntry(dst string, r io.Reader, mode fs.FileMode, budget int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	perm := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(w, io.LimitReader(r, budget+1))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > budget {
		err = errors.New("it unpacks to over 2 GB")
	}
	return n, err
}

// safeSymlink makes a link to target at dst when it stays inside dir;
// one leading out of it is left out.
func safeSymlink(dir, dst, target string) error {
	if filepath.IsAbs(target) || strings.HasPrefix(target, "/") {
		return nil
	}
	resolved := filepath.Join(filepath.Dir(dst), filepath.FromSlash(target))
	rel, err := filepath.Rel(dir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	os.Remove(dst)
	return os.Symlink(target, dst)
}
