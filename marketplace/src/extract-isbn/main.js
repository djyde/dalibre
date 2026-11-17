// @ts-check
// Extract ISBN, ported from kiwidude68's calibre plugin
// (https://github.com/kiwidude68/calibre_plugins/tree/main/extract_isbn,
// scan.py and nonpdf.py). GPL-3.0-or-later.

import { bookDocs } from "../shared/booktext.js";
import { unescape } from "../shared/epub.js";

// scan.py: up to 22 characters to allow for "-10 " and "-13 " prefixes.
const RE_ISBN = /\s*([0-9\-.–­―—^ ]{9,22}[0-9xX])/gu;
const RE_STRIP_STYLE = /<style[^<]+<\/style>/g;
const RE_STRIP_MARKUP = /<[^>]+>/g;

// nonpdf.py: by the number of spine files, how many to scan from the
// start, then from the end in reverse; then the rest.
const EPUB_FILE_SCANS = [
  [15, 10, -5],
  [10, 6, -4],
  [6, 4, -2],
  [3, 2, -1],
  [2, 1, -1],
  [1, 1, 0],
];

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  /** @type {NonNullable<Dalibre.ActionResult["changes"]>} */
  const changes = [];
  let found = 0;
  let done = 0;
  for (const book of input.books) {
    dalibre.progress(done++, input.books.length);
    const file = input.files?.[book.id];
    if (!file) continue;
    let docs;
    try {
      docs = await bookDocs(file);
    } catch {
      continue;
    }
    const isbn = scanForISBN(docs.map((d) => d.html));
    if (!isbn) continue;
    found++;
    if ((book.identifiers.isbn ?? "") !== isbn) {
      changes.push({ book: book.id, metadata: { identifiers: { isbn } } });
    }
  }
  dalibre.progress(done, input.books.length);
  if (found === 0) return { message: "No ISBN found" };
  if (changes.length === 0) return { message: "The books already have the ISBNs found" };
  return { changes };
}

/**
 * get_isbn_from_non_pdf: the first files forward, the last files in
 * reverse, then the rest, stopping at the first file with an ISBN.
 * @param {string[]} spine the documents' markup
 */
export function scanForISBN(spine) {
  const scanner = new BookScanner(validPrefixes());
  const count = spine.length;
  if (count === 0) return null;
  /** @type {string[]} */
  let first = [];
  /** @type {string[]} */
  let last = [];
  /** @type {string[]} */
  let middle = [];
  for (const [min, front, rear] of EPUB_FILE_SCANS) {
    if (count >= min) {
      first = spine.slice(0, front);
      last = rear !== 0 ? spine.slice(count + rear) : [];
      middle = count - min > 0 ? spine.slice(front, rear !== 0 ? count + rear : 0) : [];
      break;
    }
  }
  for (const html of first) {
    scanner.look(preprocess(html), true);
    if (scanner.has()) break;
  }
  if (!scanner.has()) {
    for (const html of [...last].reverse()) {
      scanner.look(preprocess(html), false);
      if (scanner.has()) break;
    }
  }
  if (!scanner.has()) {
    for (const html of middle) {
      scanner.look(preprocess(html), true);
      if (scanner.has()) break;
    }
  }
  return scanner.result();
}

/** What calibre's HTMLPreProcessor leaves for the scan: entities decoded. @param {string} html */
function preprocess(html) {
  return html.replace(/&(#x[0-9a-f]+|#\d+|amp|lt|gt|quot|apos|nbsp);/gi, (e) => {
    const s = unescape(e);
    return s === "<" || s === ">" ? e : s;
  });
}

/** The validISBN13Prefix option: "977,978,979". */
function validPrefixes() {
  const s = dalibre.settings.validISBN13Prefix || "977,978,979";
  return s.split(",").map((x) => x.trim()).filter(Boolean);
}

class BookScanner {
  /** @param {string[]} prefixes */
  constructor(prefixes) {
    this.prefixes = prefixes;
    /** @type {string[]} */
    this.isbns10 = [];
    /** @type {string[]} */
    this.isbns13 = [];
  }

  result() {
    return this.isbns13[0] ?? this.isbns10[0] ?? null;
  }

  has() {
    return this.isbns13.length + this.isbns10.length > 0;
  }

  /** look_for_identifiers_in_text, for one file. @param {string} html @param {boolean} forward */
  look(html, forward) {
    // Markup out, in case of clashes with SVG covers.
    const text = html.replace(RE_STRIP_STYLE, "").replace(RE_STRIP_MARKUP, "!");
    const matches = [...text.matchAll(RE_ISBN)].map((m) => m[1]);
    if (forward) for (const m of matches) this.evaluate(m.replace(/\n/g, ""));
    else for (const m of matches.reverse()) this.evaluate(m);
  }

  /** _evaluate_isbn_match. @param {string} original */
  evaluate(original) {
    // "ISBN-10 1-4020-4136-5" matches as "-10 1-4020-4136-5".
    if (original.startsWith("-10 ") || original.startsWith("-13 ")) original = original.slice(4);
    const txt = original.replace(/[^0-9X]/g, "");
    if (/^(\d)\1{9,12}$/.test(txt)) return;
    if (txt.length === 10 && checkISBN(txt)) this.isbns10.push(txt);
    else if (txt.length === 13 && this.prefixes.includes(txt.slice(0, 3)) && checkISBN(txt)) this.isbns13.push(txt);
  }
}

/** calibre.ebooks.metadata.check_isbn. @param {string} isbn */
export function checkISBN(isbn) {
  if (isbn.length === 10) {
    let sum = 0;
    for (let i = 0; i < 9; i++) sum += (i + 1) * Number(isbn[i]);
    const check = sum % 11;
    return (check === 10 ? "X" : String(check)) === isbn[9];
  }
  if (isbn.length === 13) {
    if (!/^\d{13}$/.test(isbn)) return false;
    let sum = 0;
    for (let i = 0; i < 12; i++) sum += Number(isbn[i]) * (i % 2 ? 3 : 1);
    return (10 - (sum % 10)) % 10 === Number(isbn[12]);
  }
  return false;
}
