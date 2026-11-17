# EPUB Split

Splits sections off an EPUB into new books. Ported from Jim Miller's [EpubSplit](https://github.com/JimmXinu/EpubSplit) calibre plugin ([`epubsplit.py`](https://github.com/JimmXinu/EpubSplit/blob/main/epubsplit.py) `SplitEpub`, `FileCache`, `splitHtml`; [`epubsplit_plugin.py`](https://github.com/JimmXinu/EpubSplit/blob/main/epubsplit_plugin.py) for the split modes and details), GPL-3.0.

As in EpubSplit, the book can be split at each spine file and at each TOC entry with an anchor. The chosen sections become a new EPUB 2 book with only their files, cut at the anchors, the images, CSS and fonts they use, links to moved anchors fixed, and a TOC of their entries. The title is the first section's TOC entry, else "<title> Split"; authors, tags, languages, series, publisher, date and rating are copied, and the comments say where it was split from.

The new books get the source's library cover (its own cover image when it has none).

Run **Split EPUB…** on one book, pick sections, and make one new book or several. As in EpubSplit, how several are made (a book per section, per N sections with an orphan limit, or N books) and what is copied to them are the plugin's settings.

Differences: EPUB 3 books are accepted, using their nav when they have no NCX; text beside the dropped elements at a cut is dropped too; TOC entries can't be edited before splitting, and there are no "always include" sections or custom columns.
