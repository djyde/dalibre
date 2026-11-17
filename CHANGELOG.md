# Changelog

Each version's section is the release notes that Dalibre's update window
shows, so write them for readers of the app. `mygo build` needs a
`## <version>` section for the version in `cmd/dalibre/mygo.json`.

## 0.6.2

- Dalibre no longer quits before its window shows when calibre's last
  library is on a network drive (SMB or AFP): the welcome screen offers
  it without opening it, so it shows no book count until you open it.
- Export to folder, in a book's menu and for a selection, copies books'
  files to a folder you choose, named from their details with a calibre
  save template, with their covers and OPF if you like.
- Email books to a Kindle's Send to Kindle address: add your Kindles and
  the account to send from in Settings › Email, then choose Email to
  from a book's menu or a selection.

## 0.6.1

- A Kindle plugged in as a USB drive (the ones before 2024) is a library
  of its own, below the recent libraries: browse, search and sort its
  books, send books to it, and remove them, in the library calibre keeps
  on it.
- Your library's books that are on a reader plugged in wear its name in
  the shelf and the list, and a book's details send it to the reader or
  remove it from there.
- A Kindle whose drive isn't mounted shows as Not mounted: choosing it
  mounts it and opens its library.
- A plugin that doesn't load offers the update that may fix it.
- dalibre-plugin downloads a book once, even when the plugin fetches it
  with a program such as pandoc.

## 0.6.0

- Open, in a book's details, opens it in the editor or in your system's
  app for its format.
- Plugins can use programs such as pandoc: Dalibre downloads them for
  you, or uses the ones Homebrew, winget or Scoop installed.
- Install from GitHub shows a plugin's README.
- Plugins go through the proxy set in your system's settings.
- The editor shows a book's images on Windows.

## 0.5.1

- Edit book opens AZW3 books, to read in Glance or to edit, and saves
  them back as AZW3.

## 0.5.0

- Plugin authors can install dalibre-plugin, the tool that makes and
  checks plugins, from npm: `npm install -g dalibre-plugin`.
- Chinese Text Conversion converts all of a book's text, not just its
  title, authors and contents, in books that write it as HTML entities.

## 0.4.0

- Plugins have their own window, from the toolbar's Plugins button, with
  a Marketplace of calibre's most used plugins and the community's.
- Plugin actions run on the books you choose or the whole library, from
  the book menu or the sidebar's Tools.
- Removing books works in libraries made before calibre 8.

## 0.3.0

- File type plugins work on books' files, as calibre's do: when books are
  added, or from a book's menu.
- AI agents can write, try and install plugins over MCP.
- Plugins use standard APIs (fetch, timers, node:fs) and can be written in
  TypeScript; dalibre.fetch and dalibre.log are removed.
- Plugin secrets such as API keys need to be entered again.
- Dalibre checks for updates every hour.

## 0.2.2

- Edit an EPUB, KEPUB or TXT book's text: choose Edit book from a book's
  menu, or Edit on one of its formats.
- Fetch metadata finds books on Google Books.
- Choose Light or Dark in Settings › General, or follow the system.

## 0.2.1

- Send books to KOReader over Wi‑Fi: turn on Wireless devices in the
  Sharing button, then choose Calibre › Connect in KOReader.
- On macOS, closing the window keeps Dalibre running; click its Dock icon
  to bring it back.

## 0.2.0

- Let AI agents such as Claude Code, Claude Desktop and Cursor use your
  library over MCP: turn it on from the MCP button in the toolbar.
- Sharing over OPDS moves to its own button in the toolbar.

## 0.1.1

- Report a bug or request a feature from Feedback in the sidebar.

## 0.1.0

- The first release of Dalibre: browse, search and edit your calibre
  library, and get new books from Project Gutenberg and plugins.
