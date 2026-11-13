# Readers plugged in

A reader plugged into the computer is a library of its own: it is listed
in the library switcher, below the recent libraries, and choosing it shows
its books on the same shelf and list as the library's, with their own
search, sort and format filter. The library stays open behind it, to send
books from and to tell which of the reader's books it has.

Today: Kindles that connect as a USB drive (USB mass storage, the Kindles
before 2024), on Linux, macOS and Windows. The design takes MTP (Kindles
since 2024) and other readers (Kobo) without changing the app.

## What the user can do

- **See the reader's books**, with the covers calibre keeps for them, the
  free space in the switcher, and in each book's detail whether the
  library has it (by calibre uuid, else by title and first author, as
  calibre matches).
- **Send books** from the library's book menu (Send to Kindle), each in
  the first format the reader takes of those the book has (AZW3, MOBI,
  AZW, …; EPUB, FB2, CBZ and DJVU too on a Kindle with KOReader), or by
  dropping files on the reader's page. Books in no format the reader
  takes are said; there is no conversion yet.
- **Remove books** from the reader (book menu, detail, Delete key), after
  asking, with the notes and reading position the reader keeps for them.
  From the library's book menu, Remove from Kindle removes the copies of
  the books chosen.
- **Add the reader's books to the library**, with the metadata the reader
  has of them.
- **Eject** the reader from the toolbar (udisks on Linux, diskutil on
  macOS; on Windows, the system's Safely Remove Hardware).

## calibre compatibility

The reader's library is kept exactly as calibre keeps it, so a reader
goes from calibre to Dalibre and back with its library:

- `metadata.calibre`, at the reader's root, is calibre's record of the
  books: Dalibre reads it, adds the books it finds that calibre didn't
  record (reading their metadata and cover from the file), drops those
  whose file is gone, and adds the books it sends, all in calibre's JSON
  (its json_codec: every field it writes, dates in its format, "None" for
  an unknown date, thumbnails as `[width, height, base64]`). Entries are
  rewritten with the fields Dalibre doesn't know as they were.
- `driveinfo.calibre` keeps the reader's id (`device_store_uuid`), which
  Dalibre uses too; it records the connection and the library sending
  (`last_library_uuid`), as calibre does, keeping calibre's other fields.
- Books go where calibre puts them: its device save template
  `{author_sort}/{title} - {authors}` under `documents/`, each part an
  ASCII file name as calibre's `ascii_filename` makes it, shortened as its
  `shorten_components_to` does (tested against paths calibre wrote).
- Removing a book removes its `.sdr` folder and the Kindle's older
  sidecar files (`.apnx`, `.mbp`, …), and the author's folder when empty,
  as calibre's Kindle driver does.
- Sending a MOBI or AZW3 that names itself (EXTH ASIN and cdetype) writes
  its cover to `system/thumbnails/thumbnail_<asin>_<cdetype>_portrait.jpg`
  for the home screen, as calibre does. calibre also writes the ASIN and
  cdetype into files that lack them, so that the Kindle shows their
  covers; Dalibre doesn't modify book files yet.

Like calibre, opening a reader writes these two files: `driveinfo.calibre`
on every connection, `metadata.calibre` when the files changed since it
was written.

## Code

```
internal/device/          the layer: Storage, Source, Driver, Device, Watcher
  volume.go               Storage of a mounted folder (os.Root: never out of it)
  volumes_<os>.go         Source of the mounted USB drives; eject
  watch.go                polls the sources, opens what a driver matches
  usbms/                  calibre's library on a reader that is a storage
                          of files (its USBMS driver): metadata.calibre,
                          driveinfo.calibre, naming, scan, send, delete
  kindle/                 the Kindle driver, on usbms
internal/app/devices.go   the readers in the app: page state, send, remove
internal/app/view_devices.go  the page, its sidebar, toolbar, detail, menus
```

Three seams keep the next readers small:

- **A `device.Storage` is how files are reached**, with names relative to
  the reader's root. `device.Volume` is a mounted folder. `usbms` only
  uses the Storage (`LocalPath` is a shortcut, `""` when there is no
  path: files are then read through `Open`), so it runs unchanged on
  another kind of storage.
- **A `device.Source` lists the storages connected.** `device.Volumes`
  lists the mounted USB drives (on Linux with their USB ids from sysfs;
  `USBInfo.Protocol` is `"usbms"`).
- **A `device.Driver` recognises a reader and opens it as a
  `device.Device`**, the interface the app uses.

### Adding MTP (Kindles since 2024)

1. Write an MTP `Storage` (over libusb through purego, keeping the build
   free of cgo, or over GNOME's gvfs mount on Linux as a first step), with
   `USB().Protocol = "mtp"` and `LocalPath` returning `""`.
2. Write a `Source` listing the MTP devices' storages, and add it to the
   watcher's sources in `App.startDevices`.
3. The Kindle driver matches by Lab126's vendor id and the `documents/`
   and `system/` folders, so it takes the MTP Kindle as it is, unless its
   storage differs (then give the driver a Config for it).

### Adding a Kobo

Write `internal/device/kobo`: a Driver matching `.kobo/KoboReader.sqlite`,
opening `usbms.Open` with Kobo's Config (books in the root, its formats,
KEPUB first), plus what Kobo needs beyond calibre's files: its shelves and
reading state are in `KoboReader.sqlite`. Add it to `App.deviceDrivers`.

## Not done yet

- Conversion on the way to the reader (EPUB → AZW3 for a stock Kindle);
  see "Capability: Convert" in docs/plugins/capabilities.md.
- Writing ASIN and cdetype into MOBI/AZW3 files sent, for their covers.
- Collections, annotations (`My Clippings.txt`), page numbers (APNX).
- The USB ids of drives on macOS and Windows (they are matched by what
  they hold), and mounting a reader that is plugged in but not mounted.
- The page's screens in the Figma file: they reuse the library's
  components, and need their design pass.
