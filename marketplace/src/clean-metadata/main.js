// @ts-check
// Clean Metadata, ported from the calibre plugin Clean Metadata 0.0.6 by
// WS64 (GPL v3), https://www.mobileread.com/forums/showthread.php?t=249929
// (main.py: process_umlaut, process_titles, process_series,
// process_authors, process_authortitle, process_dubauthor).

const OPERATIONS = [
  { value: "umlaut", title: "Cleanup German umlauts in titles" },
  { value: "titles", title: "Cleanup titles" },
  { value: "series", title: "Get series info from title" },
  { value: "authors", title: "Cleanup authors" },
  { value: "authortitle", title: "Swap author and title if title matches an existing author" },
  { value: "dubauthor", title: "Find duplicate author candidates" },
];

/** ae → ä and so on; then, when the title changed, the plugin's exceptions, in its order. */
const UMLAUT_FIRST = [["ae", "ä"], ["oe", "ö"], ["ue", "ü"], ["Ae", "Ä"], ["Oe", "Ö"], ["Ue", "Ü"]];
const UMLAUT_EXCEPTIONS = [
  ["aü", "aue"], ["eü", "eue"], ["xü", "xue"], ["ä( |$|,|!|;)", "ae$1"], ["ö( |$|,|!|;)", "oe$1"],
  ["ü( |$|,|!|;)", "ue$1"], ["(q|Q)ü", "$1ue"], ["(g|d|G|D)ös", "$1oes"], ["(bl|Bl|tr|Tr)üs", "$1ues"],
  ["äl( |i|$|,|!|;)", "ael$1"], ["Öt", "Oet"], ["(P|p)ö", "$1oe"], ["Höne", "Hoene"], ["(d|D)ü(l|t)", "$1ue$2"],
  ["(z|Z)ünde", "$1uende"], ["tül", "tuel"], ["ürill", "uerill"], ["ürit", "uerit"], ["ütte", "uette"],
  ["(z|Z)ürs", "$1uers"], ["Färie", "Faerie"], ["Göthe", "Goethe"], ["Jös", "Joes"], ["Menue", "Menü"],
  ["Mästr", "Maestr"], ["Bullerbue", "Bullerbü"], ["ichäla", "ichaela"], ["mpoer", "mpör"], ["güla", "guela"],
  ["mäl", "mael"], ["üva", "ueva"], ["erös", "eroes"], ["igül", "iguel"], ["amül", "amuel"], ["Cär", "Caer"],
  ["Nathän", "Nathaen"], ["Lemül", "Lemuel"], ["Präsep", "Praesep"], ["hüs", "hues"], ["Grülf", "Gruelf"],
  ["hüs", "hues"], ["Dädalus", "Daedalus"], ["Rüpp", "Ruepp"], ["Blübelle", "Bluebelle"], ["Säculum", "Saeculum"],
  ["Cadfäls", "Cadfaels"], ["eagues", "eagüs"], ["huette", "hütte"], ["huete", "hüte"],
].map(([re, to]) => /** @type {[RegExp, string]} */ ([new RegExp(re, "g"), to]));

/** The words the plugin strips from the end of titles. */
const STRIP = "((eine?|a) )?(v[0-9]|eine biographie|Piper Taschenbuch|Psycho-Thriller|insel taschenbuch|Erweiterte Ausgabe|fantasy|science fiction|kurzgeschichten|sammelband|ebook|heyne fliegt|ebook special|taschenbuch|sonderausgabe|gratis|umsonst|knaur hc|knaur tb|german edition|gesammelte werke|suhrkamp taschenbuch|Stories|Roman|Historischer Kriminalroman|Erotischer Roman|Liebesroman|Erzählungen|Erzaehlungen|Aktionspreis|historischer Roman|Novel|Novelle|Kriminalroman|Thriller|Psychothriller|gesamtausgabe|Australien-Roman|txt|pdf|docx?|epub|mobi|zip|docx?|epub|ebup|mobi|zip|rtf)";
const STRIP_RES = [
  new RegExp(`( ?[:|/.] ?| - ?)${STRIP}$`, "i"),
  new RegExp(` ?\\(${STRIP}\\)$`, "i"),
  new RegExp(` ?\\[${STRIP}\\]$`, "i"),
  new RegExp(` ?\\{${STRIP}\\}$`, "i"),
];
const ARTICLE_LAST = /^(.*), (eine?|der|die|das|the|a)$/i;
const SUFFIXES = ["JR", "SR", "JR.", "SR."];

/**
 * @typedef {{ title: string, authors: string[], series?: string, seriesIndex?: number }} Work
 * @typedef {{ german: boolean, keepSeries: boolean, firstLetter: boolean }} Options
 */

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  const a = await dalibre.form({
    submit: "Clean",
    fields: [
      { key: "operations", title: "Clean", type: "multiselect", value: ["titles", "authors"], options: OPERATIONS },
      { key: "german", title: "Umlauts: limit to German books", type: "toggle", value: false },
      { key: "keepSeries", title: "Series: ignore books with existing series info", type: "toggle", value: false },
      { key: "firstLetter", title: "Duplicate authors: just use first letter of firstname", type: "toggle", value: false },
    ],
  });
  const ops = new Set(/** @type {string[]} */ (a.operations ?? []));
  if (!ops.size) throw dalibre.error("invalid", "Choose at least one cleanup.");
  /** @type {Options} */
  const o = { german: a.german === true, keepSeries: a.keepSeries === true, firstLetter: a.firstLetter === true };

  // Swapping and duplicate authors look at every author in the library.
  /** @type {Dalibre.BookInfo[]} */
  const library = [];
  if (action === "clean-library" || ops.has("authortitle") || ops.has("dubauthor")) {
    let n = 0;
    for await (const b of dalibre.library.books()) {
      library.push(b);
      if (++n % 100 === 0) dalibre.progress(n, 0);
    }
  }
  const books = action === "clean-library" ? library : input.books;

  /** @type {Map<string, number>} author, lower case → number of books */
  const authorBooks = new Map();
  for (const b of library) for (const au of authorsOf(b)) {
    const k = au.toLowerCase();
    authorBooks.set(k, (authorBooks.get(k) ?? 0) + 1);
  }

  const changes = [];
  let i = 0;
  for (const book of books) {
    dalibre.progress(++i, books.length);
    /** @type {Work} */
    const w = { title: book.title ?? "", authors: authorsOf(book), series: book.series, seriesIndex: book.seriesIndex };
    if (ops.has("umlaut")) umlaut(w, book, o);
    if (ops.has("titles")) titles(w);
    if (ops.has("series")) series(w, book, o);
    if (ops.has("authors")) cleanAuthors(w);
    if (ops.has("authortitle")) authorTitle(w, authorBooks);

    /** @type {NonNullable<Dalibre.Changes>} */
    const metadata = {};
    if (w.title && w.title !== book.title) metadata.title = w.title;
    if (w.authors.join("\n") !== authorsOf(book).join("\n")) metadata.authors = w.authors;
    if (w.series && w.series !== book.series) metadata.series = w.series;
    if (w.seriesIndex !== undefined && w.seriesIndex !== book.seriesIndex) metadata.seriesIndex = w.seriesIndex;
    if (Object.keys(metadata).length) changes.push({ book: book.id, metadata });
  }

  const groups = ops.has("dubauthor") ? duplicateAuthors(books, library, o) : [];
  if (!changes.length && !groups.length) return { message: "No matching books found" };
  /** @type {Dalibre.ActionResult} */
  const result = { message: `${changes.length} book(s) (out of ${books.length}) to update` };
  if (changes.length) result.changes = changes;
  if (groups.length) {
    result.groups = groups.slice(0, 2000);
    if (!changes.length) result.message = `${groups.length} duplicate author ${groups.length === 1 ? "candidate" : "candidates"}`;
  }
  return result;
}

/** @param {Dalibre.BookInfo} b */
function authorsOf(b) {
  return [...(b.authors ?? [])];
}

/** The plugin puts the author into its patterns as it is; here it is escaped. @param {string} s */
function escape(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** process_umlaut. @param {Work} w @param {Dalibre.BookInfo} book @param {Options} o */
function umlaut(w, book, o) {
  if (o.german && !(book.languages ?? []).some((l) => l === "deu" || l === "ger" || l === "German")) return;
  const before = w.title;
  let s = before;
  for (const [from, to] of UMLAUT_FIRST) s = s.split(from).join(to);
  if (s === before) return;
  for (const [re, to] of UMLAUT_EXCEPTIONS) s = s.replace(re, to);
  w.title = s;
}

/** process_titles, run again on its result until nothing changes. @param {Work} w */
function titles(w) {
  for (let pass = 0; pass < 20; pass++) {
    const before = w.title;
    let s = before.replace(/  /g, " ");
    s = s.replace(ARTICLE_LAST, "$2 $1");
    s = s.replace(/-ok$/i, "").replace(/-neu$/i, "");
    s = s.replace(/_/g, " ");
    s = s.replace(/-[0-9][0-9][.][0-9][0-9][.][0-9][0-9]/g, "");
    for (const re of STRIP_RES) s = s.replace(re, "");
    s = s.replace(ARTICLE_LAST, "$2 $1");
    s = s.replace(/-$/, "");
    const author = w.authors[0];
    if (author) {
      const au = escape(author);
      let m = s.match(new RegExp(`^(.*) ?- ?${au}$`, "i"));
      if (m) s = m[1];
      m = s.match(new RegExp(`^${au}: ?(.*)$`, "i"));
      if (m) s = m[1];
      m = s.match(new RegExp(`^(.*) ?- ?${au} ?- ?(.*)$`, "i"));
      if (m) s = `${m[1]} - ${m[2]}`;
      m = s.match(new RegExp(`^${au} (.*)$`, "i"));
      if (m) s = m[1];
    }
    if (s) s = s[0].toUpperCase() + s.slice(1);
    s = s.replace(/^FreeBooks? /i, "");
    s = s.replace(/  /g, " ");
    if (s === before || !s) return;
    w.title = s;
  }
}

/** process_series. @param {Work} w @param {Dalibre.BookInfo} book @param {Options} o */
function series(w, book, o) {
  if (o.keepSeries && book.series) return;
  const m = w.title.match(/([^0-9-]+) #?([0-9]+) ?(-|:) (.*)/);
  if (!m) return;
  w.title = m[4];
  w.series = m[1].replace(/(Vol|Bd|Band|Volume)[.]? ?/g, "");
  w.seriesIndex = Number(m[2]);
}

/** process_authors. @param {Work} w */
function cleanAuthors(w) {
  const authors = [...w.authors];
  for (let i = 0; i < authors.length; i++) {
    let m;
    while ((m = authors[i].match(/([^;]*); ?(.*)/))) {
      authors[i] = m[1];
      authors.push(m[2]);
    }
    m = authors[i].match(/(.*), *(.*)/);
    if (m && !SUFFIXES.includes(m[2].toUpperCase())) authors[i] = `${m[2]} ${m[1]}`;
    while ((m = authors[i].match(/(^|.*[ ])(.)[ ](.*)/))) authors[i] = `${m[1]}${m[2]}. ${m[3]}`;
    while ((m = authors[i].match(/(.*[.])([A-ZÄÖÜ].*)/))) authors[i] = `${m[1]} ${m[2]}`;
  }
  w.authors = authors;
}

/** process_authortitle. @param {Work} w @param {Map<string, number>} authorBooks */
function authorTitle(w, authorBooks) {
  if (w.authors.length !== 1) return;
  const author = w.authors[0];
  let title = w.title;
  if ((authorBooks.get(author.toLowerCase()) ?? 0) !== 1 || title === author) return;
  /** @type {string | undefined} */
  let swap;
  if (authorBooks.has(title.toLowerCase())) swap = title;
  const m = title.match(/(.*), *(.*)/);
  if (m && !SUFFIXES.includes(m[2].toUpperCase())) {
    title = `${m[2]} ${m[1]}`;
    if (authorBooks.has(title.toLowerCase())) swap = title;
  }
  if (swap) {
    w.authors = [swap];
    w.title = author;
  }
}

/** Letters whose Unicode names have " WITH STROKE" but no decomposition. */
const STROKES = /** @type {Record<string, string>} */ ({ ø: "o", ł: "l", đ: "d", ħ: "h", ŧ: "t", ƀ: "b", ɨ: "i" });

/** remove_letters: a name shrunk to compare spellings. @param {string} s @param {Options} o */
function shrink(s, o) {
  let a = s.toLowerCase().trim();
  a = a.replace(/-/g, " ");
  a = a.replace(/(^| )(dr|med|prof|der|die|das|the|a)\.? /g, "");
  a = [...a.normalize("NFD").replace(/\p{M}/gu, "")].map((c) => STROKES[c] ?? c).join("");
  a = a.replace(/[^a-z ]/g, "").trim();
  if (a.includes(" ")) {
    const m = o.firstLetter ? a.match(/^([^ ])(.*?)([^ ]+)$/) : a.match(/^([^ ]+)(.*?)([^ ]+)$/);
    if (m) a = `${m[1]} ${m[3]}`;
  }
  for (const c of "bcdefghklmnpqrstvwxzjy ") for (let k = 0; k < 3; k++) a = a.split(c + c).join(c);
  return a || "-";
}

/** process_dubauthor: books by authors whose shrunk names match. @param {Dalibre.BookInfo[]} books @param {Dalibre.BookInfo[]} library @param {Options} o */
function duplicateAuthors(books, library, o) {
  /** @type {Map<string, Set<string>>} */
  const names = new Map();
  for (const b of library) for (const au of authorsOf(b)) {
    const k = shrink(au, o);
    names.set(k, (names.get(k) ?? new Set()).add(au));
  }
  const keys = new Set();
  for (const b of books) for (const au of authorsOf(b)) {
    const k = shrink(au, o);
    if ((names.get(k)?.size ?? 0) > 1) keys.add(k);
  }
  return [...keys].map((k) => {
    const set = names.get(k) ?? new Set();
    const ids = library.filter((b) => authorsOf(b).some((au) => set.has(au)));
    return { title: [...set].join(" · "), books: ids.map((b) => ({ id: b.id, note: authorsOf(b).join(" & ") })) };
  });
}
