// @ts-check
// Search The Internet, after the calibre plugin by Grant Drake (GPL v3):
// https://github.com/kiwidude68/calibre_plugins/tree/main/search_the_internet
// Its default menu of GET searches (config.py DEFAULT_MENU_SET) and its
// token encoding (action.py). FictionDB is left out: it searches by POST,
// and Dalibre only opens links. Open Library, WorldCat, Google Books,
// Douban and The StoryGraph are added.

/** [title, url template, encoding]: tokens {title}, {author}, {isbn}. */
const MENU = /** @type {[string, string, "utf-8" | "latin-1"][]} */ ([
  ["Amazon.com for Book", "https://www.amazon.com/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.co.uk for Book", "https://www.amazon.co.uk/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.com.au for Book", "https://www.amazon.com.au/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.ca for Book", "https://www.amazon.ca/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.cn for Book", "https://www.amazon.cn/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.co.jp for Book", "https://www.amazon.co.jp/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.de for Book", "https://www.amazon.de/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.fr for Book", "https://www.amazon.fr/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.it for Book", "https://www.amazon.it/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Audible for Author", "https://www.audible.com/search?keywords={author}", "utf-8"],
  ["Audible for Book", "https://www.audible.com/search?keywords={author}+{title}", "utf-8"],
  ["Barnes and Noble for Author", "https://www.barnesandnoble.com/s/{author}/_/M-8q8", "utf-8"],
  ["Barnes and Noble for Book", "https://www.barnesandnoble.com/s/{author}+{title}/_/M-8q8", "utf-8"],
  ["Barnes and Noble for ISBN", "https://www.barnesandnoble.com/s/{isbn}/_/M-8q8", "utf-8"],
  ["Barnes and Noble for Title", "https://www.barnesandnoble.com/s/{title}/_/M-8q8", "utf-8"],
  ["Chapters.ca for Author", "https://indigo.ca/en-ca/search/?q={author}", "utf-8"],
  ["Chapters.ca for Book", "https://indigo.ca/en-ca/search/?q={author}+{title}", "utf-8"],
  ["Chapters.ca for ISBN", "https://indigo.ca/en-ca/search/?q={isbn}", "utf-8"],
  ["Chapters.ca for Title", "https://indigo.ca/en-ca/search/?q={title}", "utf-8"],
  ["Douban for Book", "https://search.douban.com/book/subject_search?search_text={author}+{title}", "utf-8"],
  ["Douban for ISBN", "https://search.douban.com/book/subject_search?search_text={isbn}", "utf-8"],
  ["EBay US", "https://www.ebay.com/sch/i.html?_nkw={author}+{title}&_sacat=267", "utf-8"],
  ["EBay UK", "https://www.ebay.co.uk/sch/i.html?_nkw={author}+{title}&_sacat=267", "utf-8"],
  ["EBooks for Author", "https://www.ebooks.com/SearchApp/SearchResults.net?term={author}&RestrictBy=author", "utf-8"],
  ["EBooks for Book", "https://www.ebooks.com/SearchApp/SearchResults.net?term={author}+{title}", "utf-8"],
  ["FantasticFiction for Author", "https://www.fantasticfiction.com/search/?searchfor=author&keywords={author}", "utf-8"],
  ["FantasticFiction for Title", "https://www.fantasticfiction.com/search/?searchfor=book&keywords={title}", "utf-8"],
  ["Fnac for Author", "https://recherche.fnac.com/Search/SearchResult.aspx?SCat=2&Search={author}", "utf-8"],
  ["Fnac for Book", "https://recherche.fnac.com/Search/SearchResult.aspx?SCat=2&Search={author}+{title}", "utf-8"],
  ["Fnac for Title", "https://recherche.fnac.com/Search/SearchResult.aspx?SCat=2&Search={title}", "utf-8"],
  ["Goodreads for Author", "https://www.goodreads.com/search/search?q={author}&search_type=books", "utf-8"],
  ["Goodreads for Book", "https://www.goodreads.com/search/search?q={author}+{title}&search_type=books", "utf-8"],
  ["Goodreads for ISBN", "https://www.goodreads.com/search/search?q={isbn}&search_type=books", "utf-8"],
  ["Goodreads for Title", "https://www.goodreads.com/search/search?q={title}&search_type=books", "utf-8"],
  ["Google Books for Book", "https://www.google.com/search?tbm=bks&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google Books for ISBN", "https://www.google.com/search?tbm=bks&q=isbn:{isbn}", "utf-8"],
  ["Google images for Book", "https://www.google.com/images?q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google images 400x300", "https://www.google.com/images?as_q={author}+%22{title}%22&tbs=isch:1,isz:lt,islt:qsvga,imgo:1&safe=off", "utf-8"],
  ["Google.com for Book", "https://www.google.com/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google.de for Book", "https://www.google.de/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google.es for Book", "https://www.google.es/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google.fr for Book", "https://www.google.fr/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google.it for Book", "https://www.google.it/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["isfdb for Author", "https://www.isfdb.org/cgi-bin/se.cgi?type=Name&arg={author}", "latin-1"],
  ["isfdb for Book", "https://www.isfdb.org/cgi-bin/edit/tp_search.cgi?TERM_1={title}&USE_1=title&OPERATOR_1=AND&TERM_2={author}&USE_2=author&OPERATOR_2=AND", "latin-1"],
  ["isfdb for ISBN", "https://www.isfdb.org/cgi-bin/se.cgi?type=ISBN&arg={isbn}", "latin-1"],
  ["isfdb for Title", "https://www.isfdb.org/cgi-bin/se.cgi?type=Fiction+Titles&arg={title}", "latin-1"],
  ["Kobo for Author", "https://www.kobobooks.com/search/search.html?q={author}&f=author", "utf-8"],
  ["Kobo for Book", "https://www.kobobooks.com/search/search.html?q={author}+{title}&f=author", "utf-8"],
  ["Kobo for Title", "https://www.kobobooks.com/search/search.html?q={title}", "utf-8"],
  ["libri.de for Author", "https://www.libri.de/shop/action/advancedSearch?action=search&nodeId=-1&binderType=Alle&languageCode=DE&person={author}", "utf-8"],
  ["libri.de for Book", "https://www.libri.de/shop/action/advancedSearch?action=search&nodeId=-1&binderType=Alle&languageCode=DE&title={title}&person={author}", "utf-8"],
  ["libri.de for Title", "https://www.libri.de/shop/action/advancedSearch?action=search&nodeId=-1&binderType=Alle&languageCode=DE&title={title}", "utf-8"],
  ["Library of Congress for Author", "https://catalog.loc.gov/cgi-bin/Pwebrecon.cgi?DB=local&Search_Arg={author}&Search_Code=NAME%40&CNT=100&hist=1&type=quick", "utf-8"],
  ["Library of Congress for ISBN", "https://catalog.loc.gov/cgi-bin/Pwebrecon.cgi?DB=local&Search_Arg={isbn}&Search_Code=STNO^*&CNT=100&hist=1&type=quick", "utf-8"],
  ["Library of Congress for Title", "https://catalog.loc.gov/cgi-bin/Pwebrecon.cgi?DB=local&Search_Arg={title}&Search_Code=TKEY^*&CNT=100&hist=1&type=quick", "utf-8"],
  ["LibraryThing for Author", "https://www.librarything.com/search.php?search={author}", "utf-8"],
  ["LibraryThing for Book", "https://www.librarything.com/search.php?search={title}+{author}", "utf-8"],
  ["LibraryThing for ISBN", "https://www.librarything.com/search.php?search={isbn}", "utf-8"],
  ["LibraryThing for Title", "https://www.librarything.com/search.php?search={title}", "utf-8"],
  ["Literature-Map like Author", "https://www.literature-map.com/{author}", "utf-8"],
  ["Lovereading for Author", "https://www.lovereading.co.uk/search?s={author}", "utf-8"],
  ["Lovereading for Book", "https://www.lovereading.co.uk/search?s={author}+{title}&view=book-results", "utf-8"],
  ["Lovereading for Title", "https://www.lovereading.co.uk/search?s={title}&view=book-results", "utf-8"],
  ["ManyBooks for Author", "https://manybooks.net/search-book?search={author}", "utf-8"],
  ["ManyBooks for Title", "https://manybooks.net/search-book?search={title}", "utf-8"],
  ["NYTimes for Author", "https://nytimes.com/search?query={author}", "utf-8"],
  ["NYTimes for Book", "https://nytimes.com/search?query={author}+{title}", "utf-8"],
  ["NYTimes for Title", "https://nytimes.com/search?query={title}", "utf-8"],
  ["Open Library for Book", "https://openlibrary.org/search?q={author}+{title}", "utf-8"],
  ["Open Library for ISBN", "https://openlibrary.org/isbn/{isbn}", "utf-8"],
  ["The StoryGraph for Book", "https://app.thestorygraph.com/browse?search_term={author}+{title}", "utf-8"],
  ["Waterstones for Author", "https://www.waterstones.com/books/search/term/{author}", "utf-8"],
  ["Waterstones for Book", "https://www.waterstones.com/books/search/term/{title}+{author}", "utf-8"],
  ["Waterstones for ISBN", "https://www.waterstones.com/books/search/term/{isbn}", "utf-8"],
  ["Waterstones for Title", "https://www.waterstones.com/books/search/term/{title}", "utf-8"],
  ["WhatShouldIReadNext for ISBN", "https://www.whatshouldireadnext.com/isbn/{isbn}", "utf-8"],
  ["Wikipedia for Author", "https://en.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia for Book", "https://en.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia for Title", "https://en.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["Wikipedia.de for Author", "https://de.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia.de for Book", "https://de.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.de for Title", "https://de.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["Wikipedia.es for Author", "https://es.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia.es for Book", "https://es.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.es for Title", "https://es.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["Wikipedia.fr for Author", "https://fr.wikipedia.org/w/index.php?title=Sp%E9cial%3ARecherche&search={author}", "utf-8"],
  ["Wikipedia.fr for Book", "https://fr.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.fr for Title", "https://fr.wikipedia.org/w/index.php?title=Sp%E9cial%3ARecherche&search={title}", "utf-8"],
  ["Wikipedia.it for Author", "https://it.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia.it for Book", "https://it.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.it for Title", "https://it.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["WorldCat for Book", "https://search.worldcat.org/search?q={author}+{title}", "utf-8"],
  ["WorldCat for ISBN", "https://search.worldcat.org/search?q=bn:{isbn}", "utf-8"],
]);

/** @param {string} _action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(_action, input) {
  const book = input.books[0];
  if (!book) throw dalibre.error("invalid", "Choose a book.");
  const a = await dalibre.form({
    submit: "Search",
    fields: [
      { key: "site", title: "Search", type: "select", value: "Amazon.com for Book", options: MENU.map(([title]) => ({ value: title })) },
    ],
  });
  const item = MENU.find(([title]) => title === a.site);
  if (!item) throw dalibre.error("invalid", "Choose a site.");
  const [, template, encoding] = item;

  // As the plugin: "&" stripped from the title; only the first author.
  const values = {
    title: (book.title ?? "").replace(/&/g, ""),
    author: book.authors?.[0] ?? "",
    isbn: book.identifiers?.isbn ?? "",
  };
  if (template.includes("{isbn}") && !values.isbn) throw dalibre.error("invalid", "This book has no ISBN.");
  const url = template.replace(/\{(title|author|isbn)\}/g, (_, k) => toSearchText(values[/** @type {keyof typeof values} */ (k)], encoding));
  return { open: url };
}

/** action.py convert_to_search_text: "&" dropped, quote_plus in the site's encoding. @param {string} text @param {string} encoding */
function toSearchText(text, encoding) {
  const t = text.replace(/&/g, "").replace(/  /g, " ");
  let out = "";
  for (const ch of t) {
    if (/[A-Za-z0-9_.\-~]/.test(ch)) out += ch;
    else if (ch === " ") out += "+";
    else if (encoding === "latin-1") {
      const code = ch.codePointAt(0) ?? 0;
      if (code < 256) out += "%" + code.toString(16).toUpperCase().padStart(2, "0"); // others are dropped, as encode(…, 'ignore')
    } else out += encodeURIComponent(ch);
  }
  return out.replace(/\+\+/g, "+");
}
