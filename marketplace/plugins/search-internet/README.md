# Search the Internet

Opens a search for a book on a book site. After calibre's [Search The Internet](https://github.com/kiwidude68/calibre_plugins/tree/main/search_the_internet) plugin by Grant Drake ([`config.py`](https://github.com/kiwidude68/calibre_plugins/tree/main/search_the_internet/config.py), [`action.py`](https://github.com/kiwidude68/calibre_plugins/tree/main/search_the_internet/action.py)), GPL-3.0: its default menu of sites and its encoding of title, author and ISBN.

Choose a book, then **Search the internet…**: a searchable list of the sites, grouped by store; choosing a row opens its search in the browser. Star a site in **Customize search sites…** to keep it under Favorites, where **Open favorites** opens them all at once — calibre's Open Group.

**Customize search sites…** is calibre's configuration dialog, as Dalibre actions:

- *In the picker* — the sites that appear; the rest are hidden (calibre's Active column)
- *Favorites* — the ones **Open favorites** opens together
- *Custom sites (JSON)* — your own, with `{title}`, `{author}` and `{isbn}` tokens:

  ```json
  [{ "site": "Bertrand", "variant": "Book", "group": "Portuguese stores",
     "url": "https://www.bertrand.pt/pesquisa?q={title}+{author}", "encoding": "utf-8" }]
  ```

  `variant` and `group` are optional (`"Book"` and `"Custom"` are the defaults); `encoding` is `"utf-8"` or `"latin-1"`, as Amazon needs. Custom sites may be on any host: the plugin only asks your browser to open them.
- *Reset* — back to the defaults

FictionDB is left out (it searches by POST; Dalibre opens links). Open Library, WorldCat, Google Books, Douban and The StoryGraph are added, and so are the Portuguese bookstores Wook and Almedina (#14), Skoob, Amazon.com.br, Wikipedia.pt, and the Brazilian bookstores Estante Virtual and Livraria da Travessa; Almedina searches from its homepage instant-search state (`?books[query]=…`). ISBN searches are hidden when the book has no ISBN. Calibre's Import/Export, menu icons, reordering and keyboard shortcuts are not ported.
