# Library format

For Dalibre's developers.

Dalibre's library format is a superset of calibre's. A Dalibre library is
a calibre library that calibre opens, reads and edits as usual; Dalibre
adds its own settings and data where calibre keeps them and ignores them.

## Rules

1. **calibre's part stays calibre's.** `metadata.db` has calibre's schema
   and nothing else: no tables or columns of ours, so calibre's schema
   upgrades and Check library never meet something they don't know. Book
   folders and files are where `books.path` and `data.name` say.
2. **What calibre knows goes where calibre puts it.** Titles, authors,
   tags, identifiers and custom columns are calibre's fields, so calibre
   shows and edits them.
3. **What only Dalibre knows goes where calibre keeps it untouched:**
   - library-wide: a row of the `preferences` table (calibre's own
     library preferences, a key and a JSON value);
   - per book, when it comes: rows of `books_plugin_data` (calibre's
     store for its plugins, by book and name), deleted with the book by
     calibre's own trigger;
   - large or often written data, when it comes: files under `.dalibre/`
     in the library folder, as `.dalibre-trash` is, keyed by the book's
     `uuid`, which survives calibre renaming or moving the book.
4. **A library without Dalibre's row is a calibre library**, and Dalibre
   uses its defaults for it. Dalibre writes the row when it creates a
   library (onboarding) and when the user changes a setting it holds; it
   doesn't write it just because a calibre library was opened.

## The `dalibre_format` preference

`preferences` row with `key = 'dalibre_format'` and a JSON object as
`val`:

```json
{"version": 1, "file_names": "original"}
```

| Field | Values | Default without the row |
|---|---|---|
| `version` | the format version that last wrote the row | 0 |
| `file_names` | `"original"`, `"ascii"` | `"original"` |

calibre loads every preference as JSON, so `val` must always be valid
JSON. A Dalibre reading the row keeps fields it doesn't know when it writes
the row back, and never lowers `version`; a field's meaning never changes
within a key, so an older Dalibre can read a newer row.

Code: [`internal/calibre/format.go`](../internal/calibre/format.go).

## File names

`file_names` says how book folders and files are named
(`Author/Title (id)/Title - Author.ext`, with calibre's length limits):

- `original` keeps titles and authors as written: `刘慈欣/三体 (3)/三体 -
  刘慈欣.epub`. This is what calibre's NoTrans plugin does. Names are cut
  at whole characters and kept under 255 bytes, since a CJK character
  takes three.
- `ascii` transliterates as calibre's `ascii_filename` does: `Liu Ci
  Xin/San Ti (3)/San Ti - Liu Ci Xin.epub`. For readers or sync tools that
  mishandle non-ASCII names.

calibre reads either. calibre without NoTrans renames a book to `ascii`
names whenever it edits that book's title or authors, so a library shared
with calibre drifts; Settings › Library › File names finds the books whose
names don't follow the setting and renames them (`Library.MisnamedBooks`,
`Library.RenameBooks`).
