# Find Duplicates

Finds books that may be the same book. Ported from calibre's [Find Duplicates](https://github.com/kiwidude68/calibre_plugins/tree/main/find_duplicates) plugin by Grant Drake ([`matching.py`](https://github.com/kiwidude68/calibre_plugins/tree/main/find_duplicates/matching.py), [`book_algorithms.py`](https://github.com/kiwidude68/calibre_plugins/tree/main/find_duplicates/book_algorithms.py), [`duplicates.py`](https://github.com/kiwidude68/calibre_plugins/tree/main/find_duplicates/duplicates.py)), GPL-3.0.

**Tools › Find duplicates…** compares by title and author, each identical, similar, soundex, fuzzy or ignored (title ignored: books grouped by author), or by an identifier's value. Each group opens as a list. The form keeps your last choices.

In the results, choose books and **Not duplicates** to keep them apart from now on. **Show not-duplicate books** lists the marks; **Forget not-duplicate marks** clears them.

Not ported: binary compare (a library action gets no files), comparing with another library, variation searches. Accents are stripped where calibre transliterates with Unidecode.
