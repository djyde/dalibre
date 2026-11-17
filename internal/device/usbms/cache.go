package usbms

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"dalibre/internal/device"
)

// calibre keeps, at the root of each storage of a reader it manages, its
// own record of the books it sees there:
//
//   - metadata.calibre: a JSON list of the books, as calibre's
//     json_codec writes them, with a small cover thumbnail each;
//   - driveinfo.calibre: the reader's id (device_store_uuid), when it was
//     last connected, and the library that last sent to it.
//
// Dalibre reads and writes both the way calibre does, so a reader goes
// from one app to the other with its library: fields Dalibre doesn't know
// are kept as they were.
const (
	cacheFile     = "metadata.calibre"
	driveInfoFile = "driveinfo.calibre"
)

// entry is a book of metadata.calibre: the fields as the file has them,
// which writing it again keeps.
type entry map[string]json.RawMessage

func readCache(st device.Storage) ([]entry, error) {
	f, err := st.Open(cacheFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var es []entry
	if err := json.Unmarshal(data, &es); err != nil {
		return nil, fmt.Errorf("%s: %w", cacheFile, err)
	}
	return es, nil
}

func writeCache(st device.Storage, es []entry) error {
	if es == nil {
		es = []entry{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(es); err != nil {
		return err
	}
	return writeFile(st, cacheFile, buf.Bytes())
}

func writeFile(st device.Storage, name string, data []byte) error {
	w, err := st.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// undefinedDate is how calibre writes a date it doesn't know.
const undefinedDate = "None"

// dateLayout is Python's isoformat() of an aware datetime, as calibre
// writes dates: in UTC, with microseconds only when there are some.
const dateLayout = "2006-01-02T15:04:05.999999-07:00"

func (e entry) str(k string) string {
	var s *string
	if json.Unmarshal(e[k], &s) != nil || s == nil {
		return ""
	}
	return *s
}

func (e entry) strs(k string) []string {
	var s []string
	if json.Unmarshal(e[k], &s) != nil {
		return nil
	}
	return s
}

func (e entry) num(k string) float64 {
	var f *float64
	if json.Unmarshal(e[k], &f) != nil || f == nil {
		return 0
	}
	return *f
}

func (e entry) date(k string) time.Time {
	s := e.str(k)
	if s == "" || s == undefinedDate {
		return time.Time{}
	}
	for _, l := range []string{dateLayout, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05.999999-07:00", "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			// calibre's undefined date is year 101.
			if t.Year() <= 101 {
				return time.Time{}
			}
			return t
		}
	}
	return time.Time{}
}

func (e entry) set(k string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte("null")
	}
	e[k] = data
}

func (e entry) setDate(k string, t time.Time) {
	if t.IsZero() {
		e.set(k, undefinedDate)
		return
	}
	e.set(k, t.UTC().Format(dateLayout))
}

// book is the entry as a device.Book.
func (e entry) book() device.Book {
	b := device.Book{
		LPath:         e.str("lpath"),
		UUID:          e.str("uuid"),
		Title:         e.str("title"),
		TitleSort:     e.str("title_sort"),
		Authors:       e.strs("authors"),
		AuthorSort:    e.str("author_sort"),
		Tags:          e.strs("tags"),
		Series:        e.str("series"),
		SeriesIndex:   e.num("series_index"),
		Publisher:     e.str("publisher"),
		Languages:     e.strs("languages"),
		Comments:      e.str("comments"),
		Rating:        int(math.Round(e.num("rating"))),
		Pubdate:       e.date("pubdate"),
		Timestamp:     e.date("timestamp"),
		LastModified:  e.date("last_modified"),
		Size:          int64(e.num("size")),
		Mime:          e.str("mime"),
		ApplicationID: int64(e.num("application_id")),
	}
	json.Unmarshal(e["identifiers"], &b.Identifiers)
	var thumb []json.RawMessage
	if json.Unmarshal(e["thumbnail"], &thumb) == nil && len(thumb) == 3 {
		var s string
		if json.Unmarshal(thumb[2], &s) == nil {
			b.Thumbnail, _ = base64.StdEncoding.DecodeString(s)
		}
	}
	return b
}

// newEntry is a book as calibre writes a new one: every field its codec
// writes, so that calibre reads it as one of its own.
func newEntry(b device.Book) entry {
	e := entry{}
	for _, k := range []string{"publication_type", "cover", "db_id", "rights", "book_producer"} {
		e.set(k, nil)
	}
	for _, k := range []string{"link_maps", "user_categories", "user_metadata"} {
		e.set(k, map[string]any{})
	}
	e.update(b)
	return e
}

// update writes b's fields over the entry's, keeping those it doesn't
// know.
func (e entry) update(b device.Book) {
	nullable := func(k, v string) {
		if v == "" {
			e.set(k, nil)
		} else {
			e.set(k, v)
		}
	}
	e.set("lpath", b.LPath)
	e.set("uuid", b.UUID)
	e.set("title", b.Title)
	e.set("title_sort", b.TitleSort)
	authors := b.Authors
	if len(authors) == 0 {
		authors = []string{"Unknown"}
	}
	e.set("authors", authors)
	e.set("author_sort", b.AuthorSort)
	sortMap := map[string]string{}
	if len(authors) == 1 {
		sortMap[authors[0]] = b.AuthorSort
	} else {
		parts := strings.Split(b.AuthorSort, " & ")
		for i, a := range authors {
			if i < len(parts) && len(parts) == len(authors) {
				sortMap[a] = parts[i]
			} else {
				sortMap[a] = a
			}
		}
	}
	e.set("author_sort_map", sortMap)
	e.set("tags", nonNil(b.Tags))
	nullable("series", b.Series)
	if b.Series == "" {
		e.set("series_index", nil)
	} else {
		e.set("series_index", b.SeriesIndex)
	}
	nullable("publisher", b.Publisher)
	e.set("languages", nonNil(b.Languages))
	nullable("comments", b.Comments)
	if b.Rating > 0 {
		e.set("rating", b.Rating)
	} else {
		e.set("rating", nil)
	}
	ids := b.Identifiers
	if ids == nil {
		ids = map[string]string{}
	}
	e.set("identifiers", ids)
	e.setDate("pubdate", b.Pubdate)
	e.setDate("timestamp", b.Timestamp)
	e.setDate("last_modified", b.LastModified)
	e.set("size", b.Size)
	e.set("mime", b.Mime)
	if b.ApplicationID > 0 {
		e.set("application_id", b.ApplicationID)
	} else if _, ok := e["application_id"]; !ok {
		e.set("application_id", nil)
	}
	e.setThumbnail(b.Thumbnail)
}

func (e entry) setThumbnail(jpg []byte) {
	if len(jpg) == 0 {
		e.set("thumbnail", nil)
		return
	}
	w, h := imageSize(jpg)
	e.set("thumbnail", []any{w, h, base64.StdEncoding.EncodeToString(jpg)})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// driveInfo is driveinfo.calibre, with the fields Dalibre doesn't know
// kept.
type driveInfo map[string]json.RawMessage

func readDriveInfo(st device.Storage) driveInfo {
	di := driveInfo{}
	f, err := st.Open(driveInfoFile)
	if err != nil {
		return di
	}
	defer f.Close()
	if data, err := io.ReadAll(f); err == nil {
		json.Unmarshal(data, &di)
	}
	if di == nil {
		di = driveInfo{}
	}
	return di
}

func (di driveInfo) str(k string) string { return entry(di).str(k) }
func (di driveInfo) set(k string, v any) { entry(di).set(k, v) }

func (di driveInfo) write(st device.Storage) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(di); err != nil {
		return err
	}
	return writeFile(st, driveInfoFile, bytes.TrimSpace(buf.Bytes()))
}

// newUUID is a random (version 4) UUID, as calibre gives books.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
