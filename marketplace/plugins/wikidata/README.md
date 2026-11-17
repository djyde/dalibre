# Wikidata

Book details and covers from [Wikidata](https://www.wikidata.org), in Fetch metadata. Ported from calibre's Wikidata metadata source 2.0.0 by Steven Dick (`__init__.py` in [its plugin package](https://plugins.calibre-ebook.com/312253.zip), [MobileRead thread](https://www.mobileread.com/forums/showthread.php?t=312253)), with calibre's title tokens ([`sources/base.py`](https://github.com/kovidgoyal/calibre/blob/master/src/calibre/ebooks/metadata/sources/base.py)), GPL-3.0.

As in calibre, it tries ever wider SPARQL queries over written works and publications until one matches: the book's Wikidata id (`wd`), its Gutenberg id, its ISBN (split as Wikidata keeps it), the exact title or alternative title by the first author; with inexact searches on, the title alone, every work by the author, and (slow search) a regex over titles. Candidates get title, authors, first publication date, series and number, instance-of and genre tags, `wd` and Gutenberg ids, ISBN (when asked) and the Wikidata image as cover, ranked by shared author and title words.

Settings mirror the plugin's options. Covers are fetched with the candidates, not only for books that already have a Wikidata id. calibre's "keep duplicates" preference is taken as off.
