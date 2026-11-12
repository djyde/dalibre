package calibre

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
)

// Dalibre's library format is a superset of calibre's: metadata.db and the
// book folders stay exactly as calibre writes them, and what Dalibre adds
// lives where calibre keeps and ignores it. docs/library-format.md is the
// specification.

// FormatKey is the key of Dalibre's row in metadata.db's preferences
// table. Its value is the JSON of Format.
const FormatKey = "dalibre_format"

// FormatVersion is the version of the format this Dalibre writes.
const FormatVersion = 1

// FileNames is how book folders and files are named.
type FileNames string

const (
	// FileNamesOriginal keeps titles and authors as written: 刘慈欣/三体 (3).
	FileNamesOriginal FileNames = "original"
	// FileNamesASCII transliterates them as calibre does: Liu Ci Xin/San Ti (3).
	FileNamesASCII FileNames = "ascii"
)

// Format is Dalibre's own settings for a library, stored in the library so
// that every computer opening it follows them.
type Format struct {
	// Version is the format version that last wrote the row; 0 when the
	// library has none, as a library calibre made.
	Version   int       `json:"version"`
	FileNames FileNames `json:"file_names"`
}

// DefaultFormat is the format of a library without Dalibre's row.
func DefaultFormat() Format { return Format{FileNames: FileNamesOriginal} }

func (f Format) valid() Format {
	if f.FileNames != FileNamesASCII {
		f.FileNames = FileNamesOriginal
	}
	return f
}

type formatState = atomic.Pointer[Format]

// loadFormat reads Dalibre's row, falling back to the defaults when it is
// missing or unreadable.
func (l *Library) loadFormat() {
	f := DefaultFormat()
	var raw string
	if err := l.db.QueryRow(`SELECT val FROM preferences WHERE key = ?`, FormatKey).Scan(&raw); err == nil {
		json.Unmarshal([]byte(raw), &f)
	}
	f = f.valid()
	l.format.Store(&f)
}

// Format is the library's Dalibre format.
func (l *Library) Format() Format {
	if f := l.format.Load(); f != nil {
		return *f
	}
	return DefaultFormat()
}

// SetFileNames saves how the library names book folders and files. It
// doesn't rename books; MisnamedBooks and RenameBooks do.
func (l *Library) SetFileNames(mode FileNames) error {
	f := l.Format()
	f.FileNames = mode
	return l.saveFormat(f.valid())
}

// saveFormat writes Dalibre's row, keeping any fields a newer Dalibre
// wrote that this one doesn't know.
func (l *Library) saveFormat(f Format) error {
	fields := map[string]json.RawMessage{}
	var raw string
	err := l.db.QueryRow(`SELECT val FROM preferences WHERE key = ?`, FormatKey).Scan(&raw)
	switch {
	case err == nil:
		json.Unmarshal([]byte(raw), &fields)
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	var old struct {
		Version int `json:"version"`
	}
	json.Unmarshal([]byte(raw), &old)
	f.Version = max(old.Version, FormatVersion) // never downgrade a newer row
	mine, _ := json.Marshal(f)
	json.Unmarshal(mine, &fields)
	val, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if _, err := l.db.Exec(`INSERT INTO preferences(key, val) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET val = excluded.val`, FormatKey, string(val)); err != nil {
		return fmt.Errorf("save library format: %w", err)
	}
	l.format.Store(&f)
	return nil
}

// BookPath is the folder of a book relative to the library root, named as
// the library's format says.
func (l *Library) BookPath(id int64, title, author string) string {
	return bookPath(id, title, author, l.Format().FileNames)
}

// BookFileName is the base name of a book's files, named as the library's
// format says.
func (l *Library) BookFileName(title, author string) string {
	return bookFileName(title, author, l.Format().FileNames)
}
