// @ts-check
// Search The Internet, after the calibre plugin by Grant Drake (GPL v3):
// https://github.com/kiwidude68/calibre_plugins/tree/main/search_the_internet
// Its default menu of GET searches (config.py DEFAULT_MENU_SET) and its
// token encoding (action.py). FictionDB is left out: it searches by POST,
// and Dalibre only opens links. Open Library, WorldCat, Google Books,
// Douban and The StoryGraph are added, and so are the Portuguese
// bookstores Wook and Almedina (#14), Skoob, Wikipedia.pt,
// Amazon.com.br (utf-8, since it rejects latin-1), and the Brazilian
// bookstores Estante Virtual and Livraria da Travessa; Almedina
// searches from its homepage instant-search state (?books[query]=…),
// not Magento's /catalogsearch/result/.
//
// What calibre's configuration dialog does, the customize action does:
// sites can be switched off (its Active column), kept as favorites that
// "Open favorites" opens together (its Open Group), and joined by custom
// sites with {title}, {author} and {isbn} tokens. The answers live in
// /data/config.json. Import/Export, menu icons, reordering and keyboard
// shortcuts are not ported.

import { readFile, writeFile } from "node:fs/promises";

/** A site search: [site, variant, group, url template, encoding]. Tokens
 * {title}, {author}, {isbn}; the variant is what it searches with. */
const SITES = /** @type {[string, string, string, string, "utf-8" | "latin-1"][]} */ ([
  ["Almedina", "Author", "Portuguese stores", "https://www.almedina.net/?books%5Bquery%5D={author}", "utf-8"],
  ["Almedina", "Book", "Portuguese stores", "https://www.almedina.net/?books%5Bquery%5D={author}+{title}", "utf-8"],
  ["Almedina", "Title", "Portuguese stores", "https://www.almedina.net/?books%5Bquery%5D={title}", "utf-8"],
  ["Amazon.ca", "Book", "Amazon", "https://www.amazon.ca/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.cn", "Book", "Amazon", "https://www.amazon.cn/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.co.jp", "Book", "Amazon", "https://www.amazon.co.jp/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.co.uk", "Book", "Amazon", "https://www.amazon.co.uk/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.com", "Book", "Amazon", "https://www.amazon.com/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.com.au", "Book", "Amazon", "https://www.amazon.com.au/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.com.br", "Book", "Amazon", "https://www.amazon.com.br/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "utf-8"],
  ["Amazon.de", "Book", "Amazon", "https://www.amazon.de/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.fr", "Book", "Amazon", "https://www.amazon.fr/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Amazon.it", "Book", "Amazon", "https://www.amazon.it/s/ref=nb_sb_noss?url=search-alias%3Dstripbooks&field-keywords={author}+{title}", "latin-1"],
  ["Audible", "Author", "Audible", "https://www.audible.com/search?keywords={author}", "utf-8"],
  ["Audible", "Book", "Audible", "https://www.audible.com/search?keywords={author}+{title}", "utf-8"],
  ["Barnes and Noble", "Author", "Barnes & Noble", "https://www.barnesandnoble.com/s/{author}/_/M-8q8", "utf-8"],
  ["Barnes and Noble", "Book", "Barnes & Noble", "https://www.barnesandnoble.com/s/{author}+{title}/_/M-8q8", "utf-8"],
  ["Barnes and Noble", "ISBN", "Barnes & Noble", "https://www.barnesandnoble.com/s/{isbn}/_/M-8q8", "utf-8"],
  ["Barnes and Noble", "Title", "Barnes & Noble", "https://www.barnesandnoble.com/s/{title}/_/M-8q8", "utf-8"],
  ["Chapters.ca", "Author", "Chapters.ca", "https://indigo.ca/en-ca/search/?q={author}", "utf-8"],
  ["Chapters.ca", "Book", "Chapters.ca", "https://indigo.ca/en-ca/search/?q={author}+{title}", "utf-8"],
  ["Chapters.ca", "ISBN", "Chapters.ca", "https://indigo.ca/en-ca/search/?q={isbn}", "utf-8"],
  ["Chapters.ca", "Title", "Chapters.ca", "https://indigo.ca/en-ca/search/?q={title}", "utf-8"],
  ["Douban", "Book", "Douban", "https://search.douban.com/book/subject_search?search_text={author}+{title}", "utf-8"],
  ["Douban", "ISBN", "Douban", "https://search.douban.com/book/subject_search?search_text={isbn}", "utf-8"],
  ["eBay UK", "Book", "eBay", "https://www.ebay.co.uk/sch/i.html?_nkw={author}+{title}&_sacat=267", "utf-8"],
  ["eBay US", "Book", "eBay", "https://www.ebay.com/sch/i.html?_nkw={author}+{title}&_sacat=267", "utf-8"],
  ["EBooks.com", "Author", "EBooks.com", "https://www.ebooks.com/SearchApp/SearchResults.net?term={author}&RestrictBy=author", "utf-8"],
  ["EBooks.com", "Book", "EBooks.com", "https://www.ebooks.com/SearchApp/SearchResults.net?term={author}+{title}", "utf-8"],
  ["Estante Virtual", "Author", "Brazilian stores", "https://www.estantevirtual.com.br/busca?q={author}", "utf-8"],
  ["Estante Virtual", "Book", "Brazilian stores", "https://www.estantevirtual.com.br/busca?q={author}+{title}", "utf-8"],
  ["Estante Virtual", "ISBN", "Brazilian stores", "https://www.estantevirtual.com.br/busca?q={isbn}", "utf-8"],
  ["Estante Virtual", "Title", "Brazilian stores", "https://www.estantevirtual.com.br/busca?q={title}", "utf-8"],
  ["FantasticFiction", "Author", "FantasticFiction", "https://www.fantasticfiction.com/search/?searchfor=author&keywords={author}", "utf-8"],
  ["FantasticFiction", "Title", "FantasticFiction", "https://www.fantasticfiction.com/search/?searchfor=book&keywords={title}", "utf-8"],
  ["Fnac", "Author", "Fnac", "https://recherche.fnac.com/Search/SearchResult.aspx?SCat=2&Search={author}", "utf-8"],
  ["Fnac", "Book", "Fnac", "https://recherche.fnac.com/Search/SearchResult.aspx?SCat=2&Search={author}+{title}", "utf-8"],
  ["Fnac", "Title", "Fnac", "https://recherche.fnac.com/Search/SearchResult.aspx?SCat=2&Search={title}", "utf-8"],
  ["Goodreads", "Author", "Goodreads", "https://www.goodreads.com/search/search?q={author}&search_type=books", "utf-8"],
  ["Goodreads", "Book", "Goodreads", "https://www.goodreads.com/search/search?q={author}+{title}&search_type=books", "utf-8"],
  ["Goodreads", "ISBN", "Goodreads", "https://www.goodreads.com/search/search?q={isbn}&search_type=books", "utf-8"],
  ["Goodreads", "Title", "Goodreads", "https://www.goodreads.com/search/search?q={title}&search_type=books", "utf-8"],
  ["Google Books", "Book", "Google", "https://www.google.com/search?tbm=bks&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google Books", "ISBN", "Google", "https://www.google.com/search?tbm=bks&q=isbn:{isbn}", "utf-8"],
  ["Google images", "Book", "Google", "https://www.google.com/images?q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google images", "Cover", "Google", "https://www.google.com/images?as_q={author}+%22{title}%22&tbs=isch:1,isz:lt,islt:qsvga,imgo:1&safe=off", "utf-8"],
  ["Google.com", "Book", "Google", "https://www.google.com/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google.de", "Book", "Google", "https://www.google.de/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google.es", "Book", "Google", "https://www.google.es/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google.fr", "Book", "Google", "https://www.google.fr/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["Google.it", "Book", "Google", "https://www.google.it/#sclient=psy&q=%22{author}%22+%22{title}%22", "utf-8"],
  ["isfdb", "Author", "ISFDB", "https://www.isfdb.org/cgi-bin/se.cgi?type=Name&arg={author}", "latin-1"],
  ["isfdb", "Book", "ISFDB", "https://www.isfdb.org/cgi-bin/edit/tp_search.cgi?TERM_1={title}&USE_1=title&OPERATOR_1=AND&TERM_2={author}&USE_2=author&OPERATOR_2=AND", "latin-1"],
  ["isfdb", "ISBN", "ISFDB", "https://www.isfdb.org/cgi-bin/se.cgi?type=ISBN&arg={isbn}", "latin-1"],
  ["isfdb", "Title", "ISFDB", "https://www.isfdb.org/cgi-bin/se.cgi?type=Fiction+Titles&arg={title}", "latin-1"],
  ["Kobo", "Author", "Kobo", "https://www.kobobooks.com/search/search.html?q={author}&f=author", "utf-8"],
  ["Kobo", "Book", "Kobo", "https://www.kobobooks.com/search/search.html?q={author}+{title}&f=author", "utf-8"],
  ["Kobo", "Title", "Kobo", "https://www.kobobooks.com/search/search.html?q={title}", "utf-8"],
  ["Libri.de", "Author", "Libri.de", "https://www.libri.de/shop/action/advancedSearch?action=search&nodeId=-1&binderType=Alle&languageCode=DE&person={author}", "utf-8"],
  ["Libri.de", "Book", "Libri.de", "https://www.libri.de/shop/action/advancedSearch?action=search&nodeId=-1&binderType=Alle&languageCode=DE&title={title}&person={author}", "utf-8"],
  ["Libri.de", "Title", "Libri.de", "https://www.libri.de/shop/action/advancedSearch?action=search&nodeId=-1&binderType=Alle&languageCode=DE&title={title}", "utf-8"],
  ["Library of Congress", "Author", "Library of Congress", "https://catalog.loc.gov/cgi-bin/Pwebrecon.cgi?DB=local&Search_Arg={author}&Search_Code=NAME%40&CNT=100&hist=1&type=quick", "utf-8"],
  ["Library of Congress", "ISBN", "Library of Congress", "https://catalog.loc.gov/cgi-bin/Pwebrecon.cgi?DB=local&Search_Arg={isbn}&Search_Code=STNO^*&CNT=100&hist=1&type=quick", "utf-8"],
  ["Library of Congress", "Title", "Library of Congress", "https://catalog.loc.gov/cgi-bin/Pwebrecon.cgi?DB=local&Search_Arg={title}&Search_Code=TKEY^*&CNT=100&hist=1&type=quick", "utf-8"],
  ["LibraryThing", "Author", "LibraryThing", "https://www.librarything.com/search.php?search={author}", "utf-8"],
  ["LibraryThing", "Book", "LibraryThing", "https://www.librarything.com/search.php?search={title}+{author}", "utf-8"],
  ["LibraryThing", "ISBN", "LibraryThing", "https://www.librarything.com/search.php?search={isbn}", "utf-8"],
  ["LibraryThing", "Title", "LibraryThing", "https://www.librarything.com/search.php?search={title}", "utf-8"],
  ["Literature-Map", "Author", "Literature-Map", "https://www.literature-map.com/{author}", "utf-8"],
  ["Livraria da Travessa", "Author", "Brazilian stores", "https://www.travessa.com.br/Busca.aspx?d=1&bt={author}&cta=00&codtipoartigoexplosao=1", "utf-8"],
  ["Livraria da Travessa", "Book", "Brazilian stores", "https://www.travessa.com.br/Busca.aspx?d=1&bt={author}+{title}&cta=00&codtipoartigoexplosao=1", "utf-8"],
  ["Livraria da Travessa", "ISBN", "Brazilian stores", "https://www.travessa.com.br/Busca.aspx?d=1&bt={isbn}&cta=00&codtipoartigoexplosao=1", "utf-8"],
  ["Livraria da Travessa", "Title", "Brazilian stores", "https://www.travessa.com.br/Busca.aspx?d=1&bt={title}&cta=00&codtipoartigoexplosao=1", "utf-8"],
  ["Lovereading", "Author", "Lovereading", "https://www.lovereading.co.uk/search?s={author}", "utf-8"],
  ["Lovereading", "Book", "Lovereading", "https://www.lovereading.co.uk/search?s={author}+{title}&view=book-results", "utf-8"],
  ["Lovereading", "Title", "Lovereading", "https://www.lovereading.co.uk/search?s={title}&view=book-results", "utf-8"],
  ["ManyBooks", "Author", "ManyBooks", "https://manybooks.net/search-book?search={author}", "utf-8"],
  ["ManyBooks", "Title", "ManyBooks", "https://manybooks.net/search-book?search={title}", "utf-8"],
  ["NYTimes", "Author", "NYTimes", "https://nytimes.com/search?query={author}", "utf-8"],
  ["NYTimes", "Book", "NYTimes", "https://nytimes.com/search?query={author}+{title}", "utf-8"],
  ["NYTimes", "Title", "NYTimes", "https://nytimes.com/search?query={title}", "utf-8"],
  ["Open Library", "Book", "Open Library", "https://openlibrary.org/search?q={author}+{title}", "utf-8"],
  ["Open Library", "ISBN", "Open Library", "https://openlibrary.org/isbn/{isbn}", "utf-8"],
  ["Skoob", "Author", "Skoob", "https://www.skoob.com.br/pt/search?searchTerm={author}&tab=author", "utf-8"],
  ["Skoob", "Book", "Skoob", "https://www.skoob.com.br/pt/search?searchTerm={author}+{title}&tab=book", "utf-8"],
  ["Skoob", "ISBN", "Skoob", "https://www.skoob.com.br/pt/search?searchTerm={isbn}&tab=book", "utf-8"],
  ["Skoob", "Title", "Skoob", "https://www.skoob.com.br/pt/search?searchTerm={title}&tab=book", "utf-8"],
  ["The StoryGraph", "Book", "The StoryGraph", "https://app.thestorygraph.com/browse?search_term={author}+{title}", "utf-8"],
  ["Waterstones", "Author", "Waterstones", "https://www.waterstones.com/books/search/term/{author}", "utf-8"],
  ["Waterstones", "Book", "Waterstones", "https://www.waterstones.com/books/search/term/{title}+{author}", "utf-8"],
  ["Waterstones", "ISBN", "Waterstones", "https://www.waterstones.com/books/search/term/{isbn}", "utf-8"],
  ["Waterstones", "Title", "Waterstones", "https://www.waterstones.com/books/search/term/{title}", "utf-8"],
  ["WhatShouldIReadNext", "ISBN", "WhatShouldIReadNext", "https://www.whatshouldireadnext.com/isbn/{isbn}", "utf-8"],
  ["Wikipedia", "Author", "Wikipedia", "https://en.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia", "Book", "Wikipedia", "https://en.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia", "Title", "Wikipedia", "https://en.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["Wikipedia.de", "Author", "Wikipedia", "https://de.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia.de", "Book", "Wikipedia", "https://de.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.de", "Title", "Wikipedia", "https://de.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["Wikipedia.es", "Author", "Wikipedia", "https://es.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia.es", "Book", "Wikipedia", "https://es.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.es", "Title", "Wikipedia", "https://es.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["Wikipedia.fr", "Author", "Wikipedia", "https://fr.wikipedia.org/w/index.php?title=Sp%E9cial%3ARecherche&search={author}", "utf-8"],
  ["Wikipedia.fr", "Book", "Wikipedia", "https://fr.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.fr", "Title", "Wikipedia", "https://fr.wikipedia.org/w/index.php?title=Sp%E9cial%3ARecherche&search={title}", "utf-8"],
  ["Wikipedia.it", "Author", "Wikipedia", "https://it.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia.it", "Book", "Wikipedia", "https://it.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.it", "Title", "Wikipedia", "https://it.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["Wikipedia.pt", "Author", "Wikipedia", "https://pt.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}", "utf-8"],
  ["Wikipedia.pt", "Book", "Wikipedia", "https://pt.wikipedia.org/w/index.php?title=Special%3ASearch&search={author}+{title}", "utf-8"],
  ["Wikipedia.pt", "Title", "Wikipedia", "https://pt.wikipedia.org/w/index.php?title=Special%3ASearch&search={title}", "utf-8"],
  ["Wook", "Author", "Portuguese stores", "https://www.wook.pt/pesquisa?keyword={author}", "utf-8"],
  ["Wook", "Book", "Portuguese stores", "https://www.wook.pt/pesquisa?keyword={author}+{title}", "utf-8"],
  ["Wook", "Title", "Portuguese stores", "https://www.wook.pt/pesquisa?keyword={title}", "utf-8"],
  ["WorldCat", "Book", "WorldCat", "https://search.worldcat.org/search?q={author}+{title}", "utf-8"],
  ["WorldCat", "ISBN", "WorldCat", "https://search.worldcat.org/search?q=bn:{isbn}", "utf-8"],
]);

/** What the user made of the list: favorites and custom sites, as the
 * customize action wrote them. Custom sites' shape, one URL with tokens:
 * { "site": "Bertrand", "variant": "Book", "group": "Portuguese stores",
 *   "url": "https://www.bertrand.pt/pesquisa?q={title}", "encoding": "utf-8" } */
const DEFAULT_CONFIG = {
  favorites: ["Wook · Book", "Almedina · Book", "Goodreads · Book"],
  off: [],
  custom: [],
};

const OPEN_FAVORITES = "Open favorites";
const DATA = "config.json";

/** @param {Dalibre.ActionInput} input */
export async function run(_action, input) {
  return _action === "customize" ? customize() : search(input);
}

/** The picker: a searchable list of the sites; choosing one opens its
 * search, and "Open favorites" opens them all, as calibre's Open Group. */
async function search(input) {
  const book = input.books[0];
  if (!book) throw dalibre.error("invalid", "Choose a book.");
  const cfg = await config();
  const isbn = book.identifiers?.isbn ?? "";
  const options = [];
  const pick = (e, starred) => options.push({ value: e.site + " · " + e.variant, title: e.site, group: starred ? "Favorites" : e.group, badge: e.variant, starred });

  const entries = visibleEntries(cfg, isbn);
  for (const e of entries) {
    if (cfg.favorites.includes(e.site + " · " + e.variant)) pick(e, true);
  }
  const favorites = options.map((o) => o.value);
  if (favorites.length > 1) {
    options.unshift({ value: OPEN_FAVORITES, title: OPEN_FAVORITES, group: "Favorites", badge: favorites.length + " searches" });
  }
  for (const e of entries) {
    if (!favorites.includes(e.site + " · " + e.variant)) pick(e, false);
  }

  const a = await dalibre.form({
    submit: "Search",
    note: "Opens the search in your browser",
    fields: [
      { key: "site", title: "Filter sites…", type: "list", options },
    ],
  });
  const open = [];
  if (a.site === OPEN_FAVORITES) {
    for (const v of favorites.slice(0, 8)) {
      open.push(urlOf(v, entries, book));
    }
    return { open };
  }
  if (!a.site) throw dalibre.error("invalid", "Choose a site.");
  return { open: urlOf(a.site, entries, book) };
}

/** The customize action, as calibre's configuration dialog: switch sites
 * off, keep favorites, add custom sites, reset to defaults. */
async function customize() {
  const cfg = await config();
  const entries = allEntries(cfg);
  const opts = entries.map((e) => ({ value: e.site + " · " + e.variant }));
  const a = await dalibre.form({
    submit: "Save",
    fields: [
      { key: "in", title: "In the picker", type: "multiselect", value: entries.filter((e) => !cfg.off.includes(e.site + " · " + e.variant)).map((e) => e.site + " · " + e.variant), options: opts },
      { key: "favorites", title: "Favorites, opened together by “" + OPEN_FAVORITES + "”", type: "multiselect", value: cfg.favorites.filter((v) => opts.some((o) => o.value === v)), options: opts },
      { key: "custom", title: "Custom sites (JSON)", type: "text", value: JSON.stringify(cfg.custom) },
      { key: "reset", title: "Reset everything to the defaults", type: "toggle", value: false },
    ],
  });
  if (a.reset) {
    await writeFile(DATA, JSON.stringify(DEFAULT_CONFIG), "utf8");
    return { message: "Search sites are back to the defaults." };
  }
  let custom = [];
  if (a.custom.trim()) {
    try {
      custom = JSON.parse(a.custom);
    } catch (e) {
      throw dalibre.error("invalid", "Custom sites isn’t valid JSON: " + e.message);
    }
    if (!Array.isArray(custom) || custom.some((c) => typeof c.site !== "string" || typeof c.url !== "string")) {
      throw dalibre.error("invalid", 'Custom sites must be a list of { "site", "url", "variant"?, "group"?, "encoding"? }.');
    }
  }
  const cfg2 = {
    favorites: /** @type {string[]} */ (a.favorites),
    off: opts.map((o) => o.value).filter((v) => !a.in.includes(v)),
    custom: custom.map((c) => ({ variant: "Book", encoding: "utf-8", ...c })),
  };
  await writeFile(DATA, JSON.stringify(cfg2), "utf8");
  return { message: "Search sites saved." };
}

/** The config, with the defaults where it says nothing. */
async function config() {
  const raw = await readFile(DATA, "utf8").catch(() => "");
  if (!raw.trim()) return { ...DEFAULT_CONFIG };
  try {
    const c = JSON.parse(raw);
    return {
      favorites: Array.isArray(c.favorites) ? c.favorites : DEFAULT_CONFIG.favorites,
      off: Array.isArray(c.off) ? c.off : [],
      custom: Array.isArray(c.custom) ? c.custom : [],
    };
  } catch {
    return { ...DEFAULT_CONFIG };
  }
}

/** Every entry, the custom sites included. */
function allEntries(cfg) {
  return [...SITES.map(([site, variant, group, url, encoding]) => ({ site, variant, group, url, encoding })), ...cfg.custom];
}

/** The entries the picker shows: not switched off, and not an ISBN search
 * the book can't make. */
function visibleEntries(cfg, isbn) {
  return allEntries(cfg).filter((e) => !cfg.off.includes(e.site + " · " + e.variant) && (isbn !== "" || !e.url.includes("{isbn}")));
}

/** The URL of entry key, from the book's details. */
function urlOf(key, entries, book) {
  const e = entries.find((x) => x.site + " · " + x.variant === key);
  if (!e) throw dalibre.error("invalid", "Choose a site.");
  // As the calibre plugin: "&" stripped from the title; only the first author.
  const values = {
    title: (book.title ?? "").replace(/&/g, ""),
    author: book.authors?.[0] ?? "",
    isbn: book.identifiers?.isbn ?? "",
  };
  return e.url.replace(/\{(title|author|isbn)\}/g, (_, k) => toSearchText(values[/** @type {keyof typeof values} */ (k)], e.encoding));
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
