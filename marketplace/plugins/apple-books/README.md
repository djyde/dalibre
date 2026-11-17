# Apple Books covers

High-resolution covers from the Apple Books store, in Fetch metadata. Ported from calibre's [Apple Books covers](https://github.com/janlarres/apple-books-covers) plugin by Jan Larres ([`__init__.py`](https://github.com/janlarres/apple-books-covers/blob/main/__init__.py)), with calibre's title and author tokens ([`sources/base.py`](https://github.com/kovidgoyal/calibre/blob/master/src/calibre/ebooks/metadata/sources/base.py)), GPL-3.0.

As in calibre, it looks the ISBN up, then searches the title (then title and author) and keeps the books whose title and author have every word, taking the largest artwork Apple has. Settings: maximum number of covers (5), store country (US) and an additional store country, as two-letter codes.

It gives covers only, as the calibre plugin does.
