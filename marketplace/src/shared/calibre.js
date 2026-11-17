// @ts-check
// calibre's metadata helpers, ported to JavaScript:
// - titlecase: src/calibre/utils/titlecase.py (John Gruber, Stuart
//   Colville, Kovid Goyal; MIT)
// - checkIsbn, normalizeIsbn, stringToAuthors: src/calibre/ebooks/metadata/__init__.py
//   (Kovid Goyal; GPL v3)
// https://github.com/kovidgoyal/calibre
//
// calibre uses ICU for case; JavaScript's toUpperCase/toLowerCase do the
// same for the scripts that have case.

// ---------------------------------------------------------------- titlecase

const SMALL = String.raw`a|an|and|as|at|but|by|en|for|if|in|of|on|or|the|to|v\.?|via|vs\.?`;
const PUNCT = "!\"#$%&'‘’()*+,\\-‒–—―./:;?@\\[\\\\\\]_`{|}~";
const W = String.raw`[\p{L}\p{N}_]`; // Python's Unicode \w

const SMALL_WORDS = new RegExp(`^(${SMALL})$`, "i");
const INLINE_PERIOD = /[a-z][.][a-z]/i;
const UC_ELSEWHERE = new RegExp(`^[${PUNCT}]*?[a-zA-Z]+[A-Z]+?`);
const CAPFIRST = new RegExp(`^[${PUNCT}]*?(${W})`, "u");
const SMALL_FIRST = new RegExp(`^([${PUNCT}]*)(${SMALL})(?!${W})`, "iu");
const SMALL_LAST = new RegExp(`(?<!${W})(${SMALL})[${PUNCT}]?$`, "iu");
const SMALL_AFTER_NUM = new RegExp(`(\\d+\\s+)(a|an|the)(?!${W})`, "giu");
const SUBPHRASE = new RegExp(`([:.;?!][ ])(${SMALL})`, "g");
const APOS_SECOND = /^[dol]{1}['‘]{1}[a-z]+$/i;
const UC_INITIALS = /^(?:[A-Z]{1}\.{1}|[A-Z]{1}\.{1}[A-Z]{1})+$/;

/** ICU capitalize: the first letter upper case, the rest lower. @param {string} s */
function capitalize(s) {
  return s ? s[0].toUpperCase() + s.slice(1).toLowerCase() : s;
}

/** calibre's titlecase(). @param {string} text */
export function titlecase(text) {
  const allCaps = text.toUpperCase() === text;
  const line = [];
  for (let word of text.split(/(\s+)/)) {
    if (!word) continue;
    if (/^\s/.test(word)) {
      line.push(word);
      continue;
    }
    if (allCaps) {
      if (UC_INITIALS.test(word)) {
        line.push(word);
        continue;
      }
      word = word.toLowerCase();
    }
    if (APOS_SECOND.test(word)) {
      word = word.replace(word[0], word[0].toUpperCase());
      word = word.slice(0, 2) + word[2].toUpperCase() + word.slice(3);
      line.push(word);
      continue;
    }
    if (INLINE_PERIOD.test(word) || UC_ELSEWHERE.test(word)) {
      line.push(word);
      continue;
    }
    if (SMALL_WORDS.test(word)) {
      line.push(word.toLowerCase());
      continue;
    }
    line.push(word.split("-").map((item) => item.replace(CAPFIRST, (m) => m.toUpperCase())).join("-"));
  }
  let result = line.join("");
  result = result.replace(SMALL_FIRST, (_, p, s) => p + capitalize(s));
  result = result.replace(SMALL_AFTER_NUM, (_, n, s) => n + capitalize(s));
  result = result.replace(SMALL_LAST, (m) => capitalize(m));
  result = result.replace(SUBPHRASE, (_, p, s) => p + capitalize(s));
  return result;
}

// ---------------------------------------------------------------- ISBN

/** @param {string} isbn */
function checkDigit10(isbn) {
  let sum = 0;
  for (let i = 0; i < 9; i++) sum += (i + 1) * Number(isbn[i]);
  const check = sum % 11;
  return check === 10 ? "X" : String(check);
}

/** @param {string} isbn */
function checkDigit13(isbn) {
  let sum = 0;
  for (let i = 0; i < 12; i++) sum += (i % 2 === 0 ? 1 : 3) * Number(isbn[i]);
  const check = 10 - (sum % 10);
  return String(check === 10 ? 0 : check);
}

/** calibre's check_isbn: the ISBN's digits when valid, else null. @param {string | undefined | null} isbn */
export function checkIsbn(isbn) {
  if (!isbn) return null;
  const d = isbn.toUpperCase().replace(/[^0-9X]/g, "");
  if (d.length !== 10 && d.length !== 13) return null;
  if (/^(\d)\1{9,12}$/.test(d)) return null;
  if (d.length === 10) return /^\d{9}/.test(d) && checkDigit10(d) === d[9] ? d : null;
  return /^\d{12}/.test(d) && checkDigit13(d) === d[12] ? d : null;
}

/** calibre's normalize_isbn: a valid ISBN as ISBN-13, else unchanged. @param {string} isbn */
export function normalizeIsbn(isbn) {
  if (!isbn) return isbn;
  const ans = checkIsbn(isbn);
  if (ans === null) return isbn;
  if (ans.length === 10) {
    const core = "978" + ans.slice(0, 9);
    return core + checkDigit13(core);
  }
  return ans;
}

// ---------------------------------------------------------------- authors

/** calibre's default authors_split_regex tweak. */
const AUTHOR_PAT = /,?\s+(and|with)\s+/gi;

/** calibre's string_to_authors: "A & B", "A and B", "A with B". @param {string} raw */
export function stringToAuthors(raw) {
  if (!raw) return [];
  raw = raw.replace(/&&/g, "￿").replace(AUTHOR_PAT, "&");
  return raw.split("&").map((a) => a.trim().replace(/￿/g, "&")).filter(Boolean);
}

/** Authors, trimmed. @param {string[] | undefined} authors */
export function authorsToList(authors) {
  return (authors ?? []).map((a) => a.trim());
}

/** Unidecode stand-in: accents stripped, other scripts kept. @param {string} s */
export function decode(s) {
  return s.normalize("NFKD").replace(/\p{M}/gu, "").normalize("NFC");
}

/** Quality Check's get_formatted_author_initials (helpers.py, Grant Drake, GPL v3): "A.B.", "A. B.", "A B" or "AB". @param {string} mode @param {string} author */
export function formattedInitials(mode, author) {
  const ignore = new Set(["von", "van", "jr", "jr.", "sr", "sr.", "st", "st.", "ed", "ed.", "dr", "dr.", "phd", "ph.d", "ph.d."]);
  const ignoreUpper = new Set(["ii", "iii"]);
  /** @type {string[]} */
  const parts = [];
  let appendToPrevious = false;
  for (const tok of author.split(/\s+/)) {
    if (!tok) continue;
    let handled = false;
    if (ignore.has(tok.toLowerCase()) && tok.toUpperCase() !== tok) {
      // left as it is
    } else if (ignoreUpper.has(tok.toLowerCase())) {
      // left as it is
    } else if (/^\p{N}+$/u.test(tok)) {
      // left as it is
    } else if (tok.includes(".") || (tok.toUpperCase() === tok && tok.length <= 2)) {
      const letters = [...tok.replace(/\./g, "")];
      if (mode === "A.B.") {
        const t = letters.map((c) => c + ".").join("");
        if (appendToPrevious) parts[parts.length - 1] += t;
        else {
          parts.push(t);
          appendToPrevious = true;
        }
      } else if (mode === "A. B.") {
        parts.push(letters.map((c) => c + ". ").join("").trim());
      } else if (mode === "A B") {
        parts.push(letters.map((c) => c + " ").join("").trim());
      } else if (mode === "AB") {
        const t = letters.join("");
        if (appendToPrevious) parts[parts.length - 1] += t;
        else {
          parts.push(t);
          appendToPrevious = true;
        }
      }
      handled = true;
    }
    if (!handled) {
      parts.push(tok);
      appendToPrevious = false;
    }
  }
  return parts.join(" ");
}

// ---------------------------------------------------------------- sort values
// calibre's title_sort, author_to_author_sort and remove_bracketed_text
// (src/calibre/ebooks/metadata/__init__.py), with calibre's default tweaks
// (resources/default_tweaks.py).

const ARTICLES = /** @type {Record<string, string[]>} */ ({
  eng: [String.raw`A\s+`, String.raw`The\s+`, String.raw`An\s+`],
  epo: [String.raw`La\s+`, "L'", "L´"],
  spa: ["El", "La", "Lo", "Los", "Las", "Un", "Una", "Unos", "Unas"].map((a) => a + String.raw`\s+`),
  fra: [String.raw`Le\s+`, String.raw`La\s+`, "L'", "L´", "L’", String.raw`Les\s+`, String.raw`Un\s+`, String.raw`Une\s+`, String.raw`Des\s+`, String.raw`De\s+(La\s+)?`, "D'", "D´", "D’"],
  pol: [],
  ita: [String.raw`Lo\s+`, String.raw`Il\s+`, "L'", "L´", String.raw`La\s+`, String.raw`Gli\s+`, String.raw`I\s+`, String.raw`Le\s+`, String.raw`Uno\s+`, String.raw`Un\s+`, String.raw`Una\s+`, "Un'", "Un´", String.raw`Dei\s+`, String.raw`Degli\s+`, String.raw`Delle\s+`, String.raw`Del\s+`, String.raw`Della\s+`, String.raw`Dello\s+`, "Dell'", "Dell´"],
  por: ["A", "O", "Os", "As", "Um", "Uns", "Uma", "Umas"].map((a) => a + String.raw`\s+`),
  ron: ["Un", "O", "Nişte"].map((a) => a + String.raw`\s+`),
  deu: ["Der", "Die", "Das", "Den", "Ein", "Eine", "Einen", "Dem", "Des", "Einem", "Eines"].map((a) => a + String.raw`\s+`),
  nld: ["De", "Het", "Een", "'n", "'s", "Ene", "Ener", "Enes", "Den", "Der", "Des", "'t"].map((a) => a + String.raw`\s+`),
  swe: ["En", "Ett", "Det", "Den", "De"].map((a) => a + String.raw`\s+`),
  tur: [String.raw`Bir\s+`],
  afr: [String.raw`'n\s+`, String.raw`Die\s+`],
  ell: ["O", "I", "To", "Ta", "Tus", "Tis", "'Enas", "'Mia", "'Ena", "'Enan"].map((a) => a + String.raw`\s+`),
  hun: [String.raw`A\s+`, String.raw`Az\s+`, String.raw`Egy\s+`],
});
/** ISO 639-2/B codes calibre canonicalizes to the codes above. */
const LANG_ALIASES = /** @type {Record<string, string>} */ ({ ger: "deu", fre: "fra", dut: "nld", rum: "ron", gre: "ell" });

const QUOTE_PAIRS = /** @type {Record<string, string[]>} */ ({
  '"': ['"'], "'": ["'"], "“": ["”", "“"], "”": ["”", "”"], "„": ["”", "“"], "‚": ["’", "‘"], "’": ["’", "‘"],
  "‘": ["’", "‘"], "‹": ["›"], "›": ["‹"], "《": ["》"], "〈": ["〉"], "»": ["«", "»"], "«": ["«", "»"], "「": ["」"], "『": ["』"],
});

/** @param {string | undefined} lang ISO 639-2; English when unknown, as calibre with an English interface. */
function titleSortPattern(lang) {
  const code = lang ? LANG_ALIASES[lang] ?? lang : "eng";
  const list = ARTICLES[code] ?? ARTICLES.eng;
  return list.length ? new RegExp(`^(${list.join("|")})`, "i") : /^$/;
}

/** @param {string} t */
function stripQuotes(t) {
  if (t && t[0] in QUOTE_PAIRS) {
    const q = t[0];
    t = t.slice(1);
    if (t && QUOTE_PAIRS[q].includes(t[t.length - 1])) t = t.slice(0, -1);
  }
  return t;
}

/** calibre's title_sort, with title_series_sorting "library_order". @param {string} title @param {string} [lang] */
export function titleSort(title, lang) {
  title = stripQuotes(title.trim());
  const m = title.match(titleSortPattern(lang));
  if (m && m[1]) title = stripQuotes(title.slice(m[1].length) + ", " + m[1]);
  return title.trim();
}

/** @param {string} src */
function removeBracketedText(src) {
  /** @type {Record<string, string>} */
  const brackets = { "(": ")", "[": "]", "{": "}" };
  /** @type {Record<string, string>} */
  const rmap = { ")": "(", "]": "[", "}": "{" };
  /** @type {Record<string, number>} */
  const counts = {};
  let total = 0;
  let out = "";
  for (const c of src) {
    if (c in brackets) {
      counts[c] = (counts[c] ?? 0) + 1;
      total++;
    } else if (c in rmap) {
      if ((counts[rmap[c]] ?? 0) > 0) {
        counts[rmap[c]]--;
        total--;
      }
    } else if (total < 1) out += c;
  }
  return out;
}

const COPYWORDS = new Set(["Agency", "Corporation", "Company", "Co.", "Council", "Committee", "Inc.", "Institute", "National", "Society", "Club", "Team", "Software", "Games", "Entertainment", "Media", "Studios"].map((w) => w.toLowerCase()));
const NAME_PREFIXES = ["Mr", "Mrs", "Ms", "Dr", "Prof"].map((w) => w.toLowerCase());
const NAME_SUFFIXES = ["Jr", "Sr", "Inc", "Ph.D", "Phd", "MD", "M.D", "I", "II", "III", "IV", "Junior", "Senior"].map((w) => w.toLowerCase());

/** calibre's author_to_author_sort, with author_sort_copy_method "comma". @param {string} author */
export function authorToAuthorSort(author) {
  if (!author) return "";
  const sauthor = removeBracketedText(author).trim();
  if (sauthor.includes(",")) return author;
  const tokens = sauthor.split(/\s+/).filter(Boolean);
  if (tokens.length < 2) return author;
  if (tokens.some((t) => COPYWORDS.has(t.toLowerCase()))) return author;
  const prefixes = new Set([...NAME_PREFIXES, ...NAME_PREFIXES.map((p) => p + ".")]);
  let first = 0;
  while (first < tokens.length && prefixes.has(tokens[first].toLowerCase())) first++;
  if (first === tokens.length) return author;
  const suffixes = new Set([...NAME_SUFFIXES, ...NAME_SUFFIXES.map((p) => p + ".")]);
  let last = tokens.length - 1;
  while (last >= first && suffixes.has(tokens[last].toLowerCase())) last--;
  if (last < first) return author;
  const suffix = tokens.slice(last + 1).join(" ");
  const atokens = [tokens[last], ...tokens.slice(first, last)];
  const numToks = atokens.length;
  if (suffix) atokens.push(suffix);
  if (numToks > 1) atokens[0] += ",";
  return atokens.join(" ");
}

/** calibre's authors_to_sort_string. @param {string[]} authors */
export function authorsToSortString(authors) {
  return authors.map(authorToAuthorSort).join(" & ");
}

