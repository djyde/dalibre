# EPUB Merge

Merges two or more EPUBs into one new book. Ported from Jim Miller's [EpubMerge](https://github.com/JimmXinu/EpubMerge) calibre plugin ([`epubmerge/epubmerge.py`](https://github.com/JimmXinu/EpubMerge/blob/main/epubmerge/epubmerge.py) `doMerge`, [`epubmerge_plugin.py`](https://github.com/JimmXinu/EpubMerge/blob/main/epubmerge_plugin.py) for the new book's details), GPL-3.0.

As in EpubMerge, each book's files go unchanged under a numbered folder, with a new EPUB 2 package and NCX. The TOC gets an entry per book with the book's own TOC under it; obfuscated fonts are decoded. The default title is the books' common series, else "<first title> Anthology"; authors, tags and languages are the books' together, the publisher when they share one.

The new book gets the first book's library cover, as in EpubMerge (its own cover image when it has none).

Select the books in order, then **Merge EPUBs…**, which asks the title. EpubMerge's preferences are the plugin's settings: TOC entry per title, copy each title's TOC, skip single-entry TOCs, flatten the TOC, include the books' comments, copy the first book's series, extra tags, the word for the default title.

Differences: books without an NCX use their EPUB 3 nav; with title entries off, the books' TOCs go at the top level; no UnMerge, and no custom columns.
