# KePub

Converts EPUBs to Kobo's KePub, adding a `.kepub` format to each book. Ported from Patrick Gaskin's [kepubify](https://github.com/pgaskin/kepubify) ([`kepub/`](https://github.com/pgaskin/kepubify/tree/master/kepub): `kobo.go`, `content.go`, `convert.go`, `opf.go`, `toc.go`, `titlepage.go`, `smartypants.go`), MIT, the converter calibre's KoboTouchExtended driver and KePub Output plugin follow.

As kepubify does, each content document gets Kobo's `div#book-columns > div#book-inner` wrappers, its style tweaks, and a `koboSpan` (`kobo.P.S`) around every sentence and image, which Kobo's highlights, bookmarks and stats need; Adobe Adept and empty Word tags go. The cover gets `cover-image`, calibre's timestamp and contributor go, hrefs are percent-encoded and links follow the manifest, and a blank first page is added when the first spine item isn't a cover or title page. Books that are already KePubs are left alone.

Run **Convert to KePub…**. Options are kepubify's, and start with the last ones used: smarten punctuation, hyphenation, fullscreen reading fixes, dummy titlepage and extra CSS.
