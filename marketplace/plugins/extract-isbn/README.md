# Extract ISBN

Finds the ISBN printed in a book and sets it as the book's ISBN. Ported from calibre's [Extract ISBN](https://github.com/kiwidude68/calibre_plugins/tree/main/extract_isbn) plugin by Grant Drake ([`scan.py`](https://github.com/kiwidude68/calibre_plugins/tree/main/extract_isbn/scan.py), [`nonpdf.py`](https://github.com/kiwidude68/calibre_plugins/tree/main/extract_isbn/nonpdf.py)), GPL-3.0.

Choose books, then **Extract ISBN**. As in calibre, it reads the first documents, then the last ones backwards (where the copyright page is), then the rest, and takes the first valid ISBN-13 (977, 978 or 979) in the first document with one, else the first ISBN-10. You review the changes before they're applied. The valid ISBN-13 prefixes are a setting, as in calibre.

Works on EPUB, KEPUB, TXT, HTMLZ and FB2. Not ported: PDF scanning.
