package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// The folders of a plugin's own folder, which its script sees through
// node:fs at /data and /work.
const (
	dataFolder = "data" // what the plugin keeps between runs
	workFolder = "work" // the files it is handed, one folder a call
)

// MaxData is how much a plugin may keep in /data.
const MaxData = 64 << 20

// DataDir is the own folder of plugin id whose plugins folder is root:
// a hidden folder beside the plugins, which Discover skips and Uninstall
// removes with the plugin.
func DataDir(root, id string) string {
	return filepath.Join(root, ".data", id)
}

// settingsFolder holds, in /data, the files of the plugin's file settings:
// /data/.settings/<key>/<name>.
const settingsFolder = ".settings"

// MaxSettingFile is the largest file a file setting takes.
const MaxSettingFile = 16 << 20

// StoreSettingFile copies src into the data folder of the plugin whose
// own folder is dataDir, as the value of its file setting key, in place
// of the one there was, and returns the path its script reads it at
// ("/data/.settings/key/name"), which is the setting's value.
func StoreSettingFile(dataDir, key, src string) (string, error) {
	st, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s isn’t a file", filepath.Base(src))
	}
	if st.Size() > MaxSettingFile {
		return "", fmt.Errorf("%s is over %d MB", filepath.Base(src), MaxSettingFile>>20)
	}
	dir := filepath.Join(dataDir, dataFolder, settingsFolder, key)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := filepath.Base(src)
	if _, err := copyFile(src, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	return path.Join("/", dataFolder, settingsFolder, key, name), nil
}

// RemoveSettingFile deletes the file of file setting key.
func RemoveSettingFile(dataDir, key string) error {
	return os.RemoveAll(filepath.Join(dataDir, dataFolder, settingsFolder, key))
}

// SettingFile is where the file of a file setting whose value is v is,
// for showing its name and size; "" when v isn't one.
func SettingFile(dataDir, v string) string {
	if !strings.HasPrefix(v, "/"+dataFolder+"/"+settingsFolder+"/") {
		return ""
	}
	return filepath.Join(dataDir, filepath.FromSlash(strings.TrimPrefix(v, "/")))
}

// allowFile approves each access of node:fs: only to /data and /work,
// and writes to /data only while it holds less than MaxData.
func (p *Plugin) allowFile(op, path string) error {
	path = filepath.ToSlash(path)
	inside := func(dir string) bool { return path == "/"+dir || strings.HasPrefix(path, "/"+dir+"/") }
	switch {
	case inside(workFolder):
		return nil
	case !inside(dataFolder):
		return fs.ErrPermission
	case op == "write" && folderSize(filepath.Join(p.dataDir, dataFolder)) >= MaxData:
		p.logf("[%s] its data folder is full (%d MB)", p.Manifest.ID, MaxData>>20)
		return syscall.ENOSPC
	}
	return nil
}

// folderSize adds up the sizes of the files in dir.
func folderSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
