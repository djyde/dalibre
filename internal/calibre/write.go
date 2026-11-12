package calibre

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Metadata is what the user edits about a book.
type Metadata struct {
	Title       string
	Authors     []string
	AuthorSort  string // computed from Authors when empty
	Series      string
	SeriesIndex float64
	Tags        []string
	Publisher   string
	Rating      int // 0–10
	Pubdate     time.Time
	Languages   []string // ISO 639-2, as "eng"
	Identifiers map[string]string
	Comments    string
}

// Metadata returns the editable metadata of a loaded book.
func (b *Book) Metadata(d *Details) Metadata {
	m := Metadata{
		Title: b.Title, Authors: append([]string(nil), b.Authors...), AuthorSort: b.AuthorSort,
		Series: b.Series, SeriesIndex: b.SeriesIndex, Tags: append([]string(nil), b.Tags...),
		Publisher: b.Publisher, Rating: b.Rating, Pubdate: b.Pubdate,
		Languages: append([]string(nil), b.Languages...), Identifiers: map[string]string{},
	}
	if d != nil {
		m.Comments = d.Comments
		for k, v := range d.Identifiers {
			m.Identifiers[k] = v
		}
	}
	return m
}

func (m *Metadata) normalize() {
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		m.Title = "Unknown"
	}
	m.Authors = cleanList(m.Authors)
	if len(m.Authors) == 0 {
		m.Authors = []string{"Unknown"}
	}
	if strings.TrimSpace(m.AuthorSort) == "" {
		m.AuthorSort = AuthorsSort(m.Authors)
	}
	m.Tags = cleanList(m.Tags)
	m.Languages = cleanList(m.Languages)
	m.Series = strings.TrimSpace(m.Series)
	m.Publisher = strings.TrimSpace(m.Publisher)
	if m.SeriesIndex <= 0 {
		m.SeriesIndex = 1
	}
	m.Rating = max(0, min(10, m.Rating))
}

func cleanList(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out
}

// itemID returns the id of the row of table whose column equals value
// (case-insensitively, as calibre's columns collate), creating it.
func itemID(tx *sql.Tx, table, column, value string, extra map[string]any) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM `+table+` WHERE `+column+` = ?`, value).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	cols, marks, args := []string{column}, []string{"?"}, []any{value}
	for k, v := range extra {
		cols, marks, args = append(cols, k), append(marks, "?"), append(args, v)
	}
	res, err := tx.Exec(`INSERT INTO `+table+`(`+strings.Join(cols, ",")+`) VALUES(`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func setMetadata(tx *sql.Tx, id int64, m *Metadata, now time.Time) error {
	if _, err := tx.Exec(`UPDATE books SET title = ?, author_sort = ?, series_index = ?, pubdate = ?, last_modified = ? WHERE id = ?`,
		m.Title, m.AuthorSort, m.SeriesIndex, formatTime(m.Pubdate), formatTime(now), id); err != nil {
		return err
	}
	type link struct {
		table, linkCol, itemTable, itemCol string
		values                             []string
		extra                              func(v string) map[string]any
		stored                             func(v string) string // the name as kept, when not v
	}
	one := func(s string) []string {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	links := []link{
		{"books_authors_link", "author", "authors", "name", m.Authors, func(v string) map[string]any { return map[string]any{"sort": AuthorSort(v)} }, storedAuthor},
		{"books_tags_link", "tag", "tags", "name", m.Tags, nil, nil},
		{"books_series_link", "series", "series", "name", one(m.Series), nil, nil},
		{"books_publishers_link", "publisher", "publishers", "name", one(m.Publisher), nil, nil},
	}
	for _, ln := range links {
		if _, err := tx.Exec(`DELETE FROM `+ln.table+` WHERE book = ?`, id); err != nil {
			return err
		}
		for _, v := range ln.values {
			var extra map[string]any
			if ln.extra != nil {
				extra = ln.extra(v)
			}
			if ln.stored != nil {
				v = ln.stored(v)
			}
			item, err := itemID(tx, ln.itemTable, ln.itemCol, v, extra)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO `+ln.table+`(book, `+ln.linkCol+`) VALUES(?, ?)`, id, item); err != nil {
				return err
			}
		}
	}
	// Languages keep their order.
	if _, err := tx.Exec(`DELETE FROM books_languages_link WHERE book = ?`, id); err != nil {
		return err
	}
	for i, code := range m.Languages {
		item, err := itemID(tx, "languages", "lang_code", code, nil)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO books_languages_link(book, lang_code, item_order) VALUES(?, ?, ?)`, id, item, i); err != nil {
			return err
		}
	}
	// Ratings: a link only for a rating above zero, as calibre stores it.
	if _, err := tx.Exec(`DELETE FROM books_ratings_link WHERE book = ?`, id); err != nil {
		return err
	}
	if m.Rating > 0 {
		var rid int64
		err := tx.QueryRow(`SELECT id FROM ratings WHERE rating = ?`, m.Rating).Scan(&rid)
		if errors.Is(err, sql.ErrNoRows) {
			res, err2 := tx.Exec(`INSERT INTO ratings(rating) VALUES(?)`, m.Rating)
			if err2 != nil {
				return err2
			}
			rid, err = res.LastInsertId()
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO books_ratings_link(book, rating) VALUES(?, ?)`, id, rid); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM identifiers WHERE book = ?`, id); err != nil {
		return err
	}
	for typ, val := range m.Identifiers {
		typ, val = strings.ToLower(strings.TrimSpace(typ)), strings.TrimSpace(val)
		if typ == "" || val == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO identifiers(book, type, val) VALUES(?, ?, ?)`, id, typ, val); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM comments WHERE book = ?`, id); err != nil {
		return err
	}
	if strings.TrimSpace(m.Comments) != "" {
		if _, err := tx.Exec(`INSERT INTO comments(book, text) VALUES(?, ?)`, id, m.Comments); err != nil {
			return err
		}
	}
	// calibre rewrites metadata.opf for dirtied books the next time it runs.
	_, err := tx.Exec(`INSERT OR IGNORE INTO metadata_dirtied(book) VALUES(?)`, id)
	return err
}

// removeUnused deletes authors, tags, series and publishers no book uses,
// as calibre does after edits and removals.
func removeUnused(tx *sql.Tx) error {
	for _, q := range []string{
		`DELETE FROM authors WHERE id NOT IN (SELECT author FROM books_authors_link)`,
		`DELETE FROM tags WHERE id NOT IN (SELECT tag FROM books_tags_link)`,
		`DELETE FROM series WHERE id NOT IN (SELECT series FROM books_series_link)`,
		`DELETE FROM publishers WHERE id NOT IN (SELECT publisher FROM books_publishers_link)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// UpdateMetadata saves m for the book and, when its title or first author
// changed, moves its folder and files to the names calibre would give them.
func (l *Library) UpdateMetadata(id int64, m Metadata) error {
	m.normalize()
	old, err := l.Book(id)
	if err != nil {
		return err
	}
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	if err := setMetadata(tx, id, &m, now); err != nil {
		return err
	}
	if err := removeUnused(tx); err != nil {
		return err
	}
	undo, err := l.relocate(tx, id, old.Path, m.Title, m.Authors[0])
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		undo()
		return err
	}
	l.writeOPFBestEffort(id)
	return nil
}

// relocate moves the book's folder and files to the names for title and
// author, inside tx, and returns a function that moves them back.
func (l *Library) relocate(tx *sql.Tx, id int64, oldPath, title, author string) (undo func(), err error) {
	undo = func() {}
	newPath := l.BookPath(id, title, author)
	type file struct{ format, name string }
	var files []file
	rows, err := tx.Query(`SELECT format, name FROM data WHERE book = ?`, id)
	if err != nil {
		return undo, err
	}
	for rows.Next() {
		var f file
		if err := rows.Scan(&f.format, &f.name); err != nil {
			rows.Close()
			return undo, err
		}
		files = append(files, f)
	}
	rows.Close()
	newName := l.BookFileName(title, author)
	if newPath == oldPath {
		same := true
		for _, f := range files {
			same = same && f.name == newName
		}
		if same {
			return undo, nil
		}
	}

	oldDir := filepath.Join(l.Root, filepath.FromSlash(oldPath))
	newDir := filepath.Join(l.Root, filepath.FromSlash(newPath))
	var moves [][2]string // done moves, to undo in reverse
	undo = func() {
		for i := len(moves) - 1; i >= 0; i-- {
			os.Rename(moves[i][1], moves[i][0])
		}
	}
	if newPath != oldPath && oldPath != "" {
		if _, err := os.Stat(oldDir); err == nil {
			if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
				return undo, err
			}
			if err := os.Rename(oldDir, newDir); err != nil {
				return undo, fmt.Errorf("move book folder: %w", err)
			}
			moves = append(moves, [2]string{oldDir, newDir})
			removeIfEmpty(filepath.Dir(oldDir), l.Root)
		}
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		undo()
		return func() {}, err
	}
	for _, f := range files {
		ext := "." + strings.ToLower(f.format)
		from, to := filepath.Join(newDir, f.name+ext), filepath.Join(newDir, newName+ext)
		if from != to {
			if _, err := os.Stat(from); err == nil {
				if err := os.Rename(from, to); err != nil {
					undo()
					return func() {}, err
				}
				moves = append(moves, [2]string{from, to})
			}
		}
	}
	if _, err := tx.Exec(`UPDATE books SET path = ? WHERE id = ?`, newPath, id); err != nil {
		undo()
		return func() {}, err
	}
	if _, err := tx.Exec(`UPDATE data SET name = ? WHERE book = ?`, newName, id); err != nil {
		undo()
		return func() {}, err
	}
	return undo, nil
}

// MisnamedBooks lists the books whose folder or file names no longer match
// their title and first author, such as books calibre added with pinyin
// names ("Liu Ci Xin/San Ti (3)" for 刘慈欣's 三体).
func (l *Library) MisnamedBooks() ([]*Book, error) {
	books, err := l.Books()
	if err != nil {
		return nil, err
	}
	names := map[int64][]string{}
	rows, err := l.db.Query(`SELECT book, name FROM data`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		names[id] = append(names[id], name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []*Book
	for _, b := range books {
		author := firstOr(b.Authors, "Unknown")
		misnamed := b.Path != l.BookPath(b.ID, b.Title, author)
		want := l.BookFileName(b.Title, author)
		for _, n := range names[b.ID] {
			misnamed = misnamed || n != want
		}
		if misnamed {
			out = append(out, b)
		}
	}
	return out, nil
}

// RenameBooks moves each book's folder and files to the names its title and
// first author give, one book at a time, so that a failure leaves the
// books before it renamed and the rest as they were. progress, if not nil,
// is called after each book.
func (l *Library) RenameBooks(ids []int64, progress func(done int)) (renamed int, err error) {
	for i, id := range ids {
		if err := l.renameBook(id); err != nil {
			return renamed, err
		}
		renamed++
		if progress != nil {
			progress(i + 1)
		}
	}
	return renamed, nil
}

func (l *Library) renameBook(id int64) error {
	b, err := l.Book(id)
	if err != nil {
		return err
	}
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	undo, err := l.relocate(tx, id, b.Path, b.Title, firstOr(b.Authors, "Unknown"))
	if err != nil {
		return fmt.Errorf("%s: %w", b.Title, err)
	}
	if err := tx.Commit(); err != nil {
		undo()
		return err
	}
	return nil
}

func removeIfEmpty(dir, root string) {
	if filepath.Clean(dir) == filepath.Clean(root) {
		return
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
}

// NewBook describes a book to add.
type NewBook struct {
	Metadata Metadata
	File     string // the e-book to copy into the library
	Cover    []byte // an image in any format image.Decode reads, or nil
}

// AddBook copies the file into the library as a new book.
func (l *Library) AddBook(nb NewBook) (int64, error) {
	m := nb.Metadata
	m.normalize()
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(nb.File), "."))
	if ext == "" {
		return 0, fmt.Errorf("%s has no extension", nb.File)
	}
	tx, err := l.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now()
	res, err := tx.Exec(`INSERT INTO books(title, series_index, author_sort, timestamp, pubdate, last_modified) VALUES(?, ?, ?, ?, ?, ?)`,
		m.Title, m.SeriesIndex, m.AuthorSort, formatTime(now), formatTime(m.Pubdate), formatTime(now))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := setMetadata(tx, id, &m, now); err != nil {
		return 0, err
	}
	rel := l.BookPath(id, m.Title, m.Authors[0])
	dir := filepath.Join(l.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
			removeIfEmpty(filepath.Dir(dir), l.Root)
		}
	}()
	name := l.BookFileName(m.Title, m.Authors[0])
	size, err := copyFile(nb.File, filepath.Join(dir, name+"."+ext))
	if err != nil {
		return 0, err
	}
	hasCover := false
	if len(nb.Cover) > 0 {
		if err := writeCover(filepath.Join(dir, "cover.jpg"), nb.Cover); err == nil {
			hasCover = true
		}
	}
	if _, err := tx.Exec(`UPDATE books SET path = ?, has_cover = ? WHERE id = ?`, rel, hasCover, id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO data(book, format, uncompressed_size, name) VALUES(?, ?, ?, ?)`, id, strings.ToUpper(ext), size, name); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	ok = true
	l.writeOPFBestEffort(id)
	return id, nil
}

// AddFormat adds or replaces one format of a book.
func (l *Library) AddFormat(id int64, file string) error {
	b, err := l.Book(id)
	if err != nil {
		return err
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(file), "."))
	name := l.BookFileName(b.Title, firstOr(b.Authors, "Unknown"))
	if err := os.MkdirAll(l.Dir(b), 0o755); err != nil {
		return err
	}
	dst := filepath.Join(l.Dir(b), name+"."+ext)
	tmp := dst + ".part"
	size, err := copyFile(file, tmp)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	tx, err := l.db.Begin()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO data(book, format, uncompressed_size, name) VALUES(?, ?, ?, ?)
		ON CONFLICT(book, format) DO UPDATE SET uncompressed_size = excluded.uncompressed_size, name = excluded.name`,
		id, strings.ToUpper(ext), size, name); err != nil {
		os.Remove(tmp)
		return err
	}
	if _, err := tx.Exec(`UPDATE books SET last_modified = ? WHERE id = ?`, formatTime(time.Now()), id); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return tx.Commit()
}

// FormatRewritten records that a format's file was written in place, as
// the book editor saves it: its new size, and the book's modified time.
func (l *Library) FormatRewritten(id int64, format string, size int64) error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE data SET uncompressed_size = ? WHERE book = ? AND format = ?`, size, id, strings.ToUpper(format)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE books SET last_modified = ? WHERE id = ?`, formatTime(time.Now()), id); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveFormat deletes one format of a book, file and record.
func (l *Library) RemoveFormat(id int64, format string) error {
	b, err := l.Book(id)
	if err != nil {
		return err
	}
	var name string
	if err := l.db.QueryRow(`SELECT name FROM data WHERE book = ? AND format = ?`, id, format).Scan(&name); err != nil {
		return err
	}
	if _, err := l.db.Exec(`DELETE FROM data WHERE book = ? AND format = ?`, id, format); err != nil {
		return err
	}
	err = os.Remove(filepath.Join(l.Dir(b), name+"."+strings.ToLower(format)))
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return err
}

// SetCover replaces the book's cover with an image in any format
// image.Decode reads; it is stored as cover.jpg.
func (l *Library) SetCover(id int64, img []byte) error {
	b, err := l.Book(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.Dir(b), 0o755); err != nil {
		return err
	}
	if err := writeCover(l.CoverPath(b), img); err != nil {
		return err
	}
	_, err = l.db.Exec(`UPDATE books SET has_cover = 1, last_modified = ? WHERE id = ?`, formatTime(time.Now()), id)
	return err
}

// TrashDir is where removed books go when no trash function is given,
// inside the library, so that a removal can be undone by hand.
const TrashDir = ".dalibre-trash"

// hasTable reports whether the library's database has table name.
func hasTable(tx *sql.Tx, name string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}

// RemoveBooks deletes books from the database, then hands each book's
// folder to trash, as the system's Recycle Bin; with a nil trash the
// folders move to the library's TrashDir.
func (l *Library) RemoveBooks(ids []int64, trash func(path string) error) error {
	type moved struct{ from, to string }
	var dirs []moved
	stamp := time.Now().Format("20060102-150405")
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var path string
		if err := tx.QueryRow(`SELECT path FROM books WHERE id = ?`, id).Scan(&path); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM books WHERE id = ?`, id); err != nil {
			return err
		}
		// Tables newer calibres added, which older libraries don't have:
		// books_pages_link came with calibre 8.
		for _, table := range []string{"books_pages_link", "metadata_dirtied", "annotations_dirtied"} {
			if ok, err := hasTable(tx, table); err != nil {
				return err
			} else if !ok {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE book = ?`, id); err != nil {
				return err
			}
		}
		if path != "" {
			dirs = append(dirs, moved{
				from: filepath.Join(l.Root, filepath.FromSlash(path)),
				to:   filepath.Join(l.Root, TrashDir, fmt.Sprintf("%s-%d", stamp, id)),
			})
		}
	}
	if err := removeUnused(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	var errs []error
	for _, d := range dirs {
		if _, err := os.Stat(d.from); err != nil {
			continue
		}
		if trash != nil {
			if err := trash(d.from); err != nil {
				errs = append(errs, err)
			} else {
				removeIfEmpty(filepath.Dir(d.from), l.Root)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(d.to), 0o755); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(d.from, d.to); err != nil {
			errs = append(errs, err)
			continue
		}
		removeIfEmpty(filepath.Dir(d.from), l.Root)
	}
	return errors.Join(errs...)
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[0]
	}
	return def
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// writeCover stores img as a JPEG, re-encoding other formats.
func writeCover(path string, img []byte) error {
	if bytes.HasPrefix(img, []byte{0xff, 0xd8, 0xff}) {
		return os.WriteFile(path, img, 0o644)
	}
	decoded, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return fmt.Errorf("decode cover: %w", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, decoded, &jpeg.Options{Quality: 90}); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
