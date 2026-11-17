// @ts-check
// Wikidata, ported from Steven Dick's calibre metadata source "Wikidata"
// 2.0.0 (__init__.py in https://plugins.calibre-ebook.com/312253.zip;
// https://www.mobileread.com/forums/showthread.php?t=312253), with
// calibre's get_title_tokens (src/calibre/ebooks/metadata/sources/base.py).
// GPL-3.0-or-later.
//
// identify() tries progressively wider and slower SPARQL queries until one
// finds something: the book's Wikidata id, its Gutenberg id, its ISBN, the
// exact title and first author; then, with "inexact" on, the title alone,
// every work by the author, and a regex over titles.

const SPARQL = "https://query.wikidata.org/sparql";

/** The plugin's options, from Dalibre's settings (toggles arrive as "true" or "false"). */
function prefs() {
  const s = dalibre.settings;
  const on = (/** @type {string} */ k, /** @type {boolean} */ d) => (s[k]?.trim() ? /^(y|yes|true|on|1)$/i.test(s[k].trim()) : d);
  const tagmode = (s.tagmode || "label").trim().toLowerCase();
  return {
    slowsearch: on("slowsearch", false),
    gutensave: on("gutensave", true),
    gutenfix: on("gutenfix", false),
    gutensearch: on("gutensearch", true),
    save_isbn: on("save_isbn", false),
    instance_tags: on("instance_tags", true),
    genre_tags: on("genre_tags", false),
    tagmode: ["label", "wdcode", "both"].includes(tagmode) ? tagmode : "label",
    lang: (s.lang || "en").trim() || "en",
    inexact: on("inexact", false),
    strip_subtitle: on("strip_subtitle", false),
    inexact_limit: parseInt(s.inexact_limit || "100", 10) || 100,
    ignorewdid: on("ignorewdid", false),
  };
}

/** @typedef {ReturnType<typeof prefs>} Prefs */

/** @param {Dalibre.Book} book @returns {Promise<Dalibre.Candidate[]>} */
export async function identify(book) {
  const p = prefs();
  const lang = p.lang;
  const identifiers = book.identifiers ?? {};
  const title = book.title || "";
  const authors = book.authors ?? [];
  /** @type {any[] | null} */
  let matches = null;
  let titleexact = true;
  let gidSaved = false;
  // calibre's "keep duplicates" preference has no counterpart: as if off.
  const insertGid = p.gutenfix;

  // A Gutenberg id, given or in a URI, in case it can be used or saved.
  let gid = identifiers.gutenberg || null;
  if (!(gid && p.gutensearch)) {
    const m = identifiers.uri && /^https?.\/\/www.gutenberg.org\/ebooks\/(\d+)/.exec(identifiers.uri);
    if (m) gid = m[1];
    if (!gid && identifiers.odid) {
      const o = /^(\d+)@pg\//.exec(identifiers.odid);
      if (o) gid = o[1];
    }
  }

  const wikidataId = identifiers.wd;
  if (wikidataId && !p.ignorewdid && /^Q\d+$/.test(wikidataId)) {
    matches = await doquery(p, "", `{ BIND(wd:${wikidataId} AS ?book) . }`);
  }
  if (!matches && p.gutensearch && gid) {
    matches = await doquery(p, `?book wdt:P2034 ${lit(gid)}. `);
  }

  // The ISBN, split as the original does (Wikidata keeps ISBNs hyphenated).
  const isbn = identifiers.isbn;
  if (!matches && isbn) {
    const m = /^(\d)(\d{3})(\d{5})(\d|X)$/.exec(isbn);
    const n = m ? null : /^(\d{3})(\d)(\d{3})(\d{5})(\d)$/.exec(isbn);
    if (m) matches = await doquery(p, `?book wdt:P957 ${lit(`${m[1]}-${m[2]}-${m[3]}-${m[4]}`)}.`);
    else if (n) matches = await doquery(p, `?book wdt:P212 ${lit(`${n[1]}-${n[2]}-${n[3]}-${n[4]}-${n[5]}`)}. `);
    else if (/(^\d{9}(\d|X)$)|(^\d{1,5}-\d{1,7}-\d{1,6}-[0-9X]$)/.test(isbn)) matches = await doquery(p, `?book wdt:P957 ${lit(isbn)}.`);
    else if (/^97[89]-([0-57]-(\d-\d{7}|\d\d-\d{6}|\d\d\d-\d{5}|\d{4}-\d{4}|\d{5}-\d\d\d|\d{6}-\d\d|\d{7}-\d)|[89]\d-(\d-\d{6}|\d\d-\d{5}|\d\d\d-\d{4}|\d{4}-\d\d\d|\d{5}-\d\d|\d{6}-\d)|[69]\d\d-(\d-\d{5}|\d\d-\d{4}|\d\d\d-\d\d\d|\d{4}-\d\d|\d{5}-\d)|99[0-8]\d-\d-\d{4}|99[0-8]\d-\d\d-\d\d\d|99[0-8]\d-\d\d\d-\d\d|99[0-8]\d-\d{4}-\d|999\d\d-\d-\d\d\d|999\d\d-\d\d-\d\d|999\d\d-\d\d\d-\d)-\d/.test(isbn)) {
      matches = await doquery(p, `?book wdt:P212 ${lit(isbn)}. `);
    }
  }

  const author = !matches && authors.length ? authors[0] : null;
  // The exact title (or an alternative title) by the first author.
  if (!matches && author && title) {
    matches = await doquery(p, `{ ?book rdfs:label ${lit(title, lang)}. } UNION { ?book skos:altLabel ${lit(title, lang)}. } ?book wdt:P50 ?author. ?author rdfs:label ${lit(author, lang)}.`);
  }
  if (!matches && p.inexact && title) {
    matches = await doquery(p, `{?book rdfs:label ${lit(title, lang)}.} UNION { ?book skos:altLabel ${lit(title, lang)}. }`);
  }
  // From here on, titles match loosely.
  if (!matches) titleexact = false;
  if (!matches && p.inexact && author) {
    matches = await doquery(p, `?book wdt:P50 ?author. ?author rdfs:label ${lit(author, lang)}. `, "", p.inexact_limit);
  }
  if (!matches && p.inexact && p.slowsearch && title) {
    matches = await doquery(p, "?book wdt:P1476 ?t.", `FILTER(REGEX(?t, ${lit(title)}, 'i'))`);
  }

  /** @type {{ c: Dalibre.Candidate, relevance: number }[]} */
  const out = [];
  if (matches) {
    // Rank by the words the authors and (for loose matches) the title share.
    const authorwords = new Set(authors.flatMap((a) => a.split(" ")));
    const titlewords = titleexact ? new Set() : new Set(getTitleTokens(title, p.strip_subtitle));
    for (const match of matches) {
      let relevance = 0;
      const wdid = /^http:\/\/www\.wikidata\.org\/entity\/(Q\d+)$/.exec(match.book?.value ?? "")?.[1];
      if (!wdid) continue;
      const mtitle = String(match.bookLabel?.value ?? wdid);
      /** @type {string[]} */
      let mauthors = [];
      if (match.authors?.value) {
        mauthors = String(match.authors.value).split("&").filter(Boolean);
        relevance += intersection(authorwords, new Set(mauthors.flatMap((a) => a.split(" "))));
      }
      if (!titleexact) relevance += intersection(titlewords, new Set(getTitleTokens(mtitle, false)));
      /** @type {Record<string, string>} */
      const ids = { wd: wdid };

      if (match.gutenbergids?.value) {
        const mgids = String(match.gutenbergids.value).split(" ");
        if (gid && mgids.includes(gid)) {
          relevance += 100;
          gidSaved = true;
        }
        // calibre keeps the last one set.
        if (p.gutensave) ids.gutenberg = mgids[mgids.length - 1];
      } else if (p.gutensave && gid && (insertGid || (title === mtitle && authors.join("&") === mauthors.join("&")))) {
        gidSaved = true;
        ids.gutenberg = gid;
      }

      /** @type {Dalibre.Candidate} */
      const c = { id: wdid, title: mtitle, authors: mauthors, identifiers: ids, url: `http://www.wikidata.org/entity/${wdid}` };
      if (/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(match.firstpub?.value ?? "")) {
        c.pubdate = match.firstpub.value;
        relevance += 1;
      }

      // Tags: instance of and genres, as labels and/or Q codes, commas out.
      const tags = new Set();
      const labels = (/** @type {string} */ k) => String(match[k]?.value ?? "").replace(/,/g, "").split("&");
      const codes = (/** @type {string} */ k) => String(match[k]?.value ?? "").split(" ").map((u) => /\/(Q\d+)$/.exec(u)?.[1]);
      if (p.instance_tags && match.instanceLabels) labels("instanceLabels").forEach((t) => tags.add(t));
      if (p.genre_tags && match.genreLabels) labels("genreLabels").forEach((t) => tags.add(t));
      if (p.instance_tags && match.instances) codes("instances").forEach((t) => tags.add(t));
      if (p.genre_tags && match.genres) codes("genres").forEach((t) => tags.add(t));
      c.tags = [...tags].filter(Boolean).sort();

      // ISBNs come only with save_isbn on; the ISBN-13 wins.
      for (const k of ["isbn10", "isbn13"]) {
        const v = checkISBN(match[k]?.value ?? "");
        if (v) ids.isbn = v;
      }

      if (match.seriesLabel?.value) c.series = String(match.seriesLabel.value);
      const ordinal = match.seriesordinal?.value;
      c.seriesIndex = ordinal !== undefined && /^\s*[-+]?\d+\s*$/.test(ordinal) ? parseInt(ordinal, 10) : 0;

      out.push({ c, relevance });
    }
  }

  // The Gutenberg id alone, as a result of its own, when no match carries it.
  if (!matches || (!insertGid && !gidSaved)) {
    const fake = fakeBook(p, title, authors, identifiers, gid);
    if (fake) out.push({ c: fake, relevance: 1000 });
  }

  await addCovers(out.map((o) => o.c));
  return out
    .map((o, i) => ({ ...o, i }))
    .sort((a, b) => b.relevance - a.relevance || a.i - b.i)
    .slice(0, 20)
    .map((o) => o.c);
}

/**
 * The original's doquery: written works and publications (and their
 * subclasses) with their label, first publication date, authors,
 * Gutenberg ids, instance of and genres, series with its ordinal, and
 * ISBNs with save_isbn.
 * @param {Prefs} p @param {string} early @param {string} [late] @param {number} [limit]
 * @returns {Promise<any[] | null>}
 */
async function doquery(p, early, late = "", limit = 10) {
  let moreselect = "";
  let moreservice = "";
  let moregroup = "";
  if (p.tagmode === "label" || p.tagmode === "both") {
    moreservice += `
            ?instance rdfs:label ?instanceLabel.
            ?genre rdfs:label ?genreLabel.`;
    moreselect += `
        (GROUP_CONCAT(DISTINCT ?instanceLabel; SEPARATOR="&") as ?instanceLabels)
        (GROUP_CONCAT(DISTINCT ?genreLabel; SEPARATOR="&") as ?genreLabels)`;
  }
  if (p.tagmode === "wdcode" || p.tagmode === "both") {
    moreselect += `
        (GROUP_CONCAT(DISTINCT ?instance; SEPARATOR=" ") as ?instances)
        (GROUP_CONCAT(DISTINCT ?genre; SEPARATOR=" ") as ?genres)`;
  }
  moreselect += "?seriesLabel ?seriesordinal ";
  moregroup += "?seriesLabel ?seriesordinal ";
  late += ` OPTIONAL { ?book p:P179 ?seriesstatement.
            ?seriesstatement ps:P179 ?series.
            OPTIONAL { ?seriesstatement pq:P1545 ?seriesordinal. }}`;
  moreservice += " ?series rdfs:label ?seriesLabel.";
  if (p.save_isbn) {
    moreselect += "?isbn10 ?isbn13 ";
    moregroup += "?isbn10 ?isbn13 ";
    late += "OPTIONAL { ?book wdt:P212 ?isbn13. } OPTIONAL { ?book wdt:P957 ?isbn10. }";
  }
  const lang = p.lang.replace(/[^\w-]/g, "");
  // The comment tells the Wikidata people who asks.
  const query = `# calibre wikidata plugin, ported to Dalibre
SELECT ?book ?bookLabel (MIN(?pubdate) as ?firstpub)
 (GROUP_CONCAT(DISTINCT ?authorLabel; SEPARATOR="&") as ?authors)
 (GROUP_CONCAT(DISTINCT ?gutenbergid; SEPARATOR=" ") as ?gutenbergids)
 ${moreselect}
 WHERE {
 SERVICE wikibase:label {
 bd:serviceParam wikibase:language "[AUTO_LANGUAGE],${lang}".
 ?book rdfs:label ?bookLabel.
 ?author rdfs:label ?authorLabel.
 ${moreservice}
 }
 VALUES ?worktype { wd:Q47461344 wd:Q732577 }
  ?book wdt:P31/wdt:P279*  ?worktype.
  ${early}
  OPTIONAL { ?book wdt:P577 ?pubdate. }
  OPTIONAL { ?book wdt:P50 ?author. }
  OPTIONAL { ?book wdt:P2034 ?gutenbergid. }
  OPTIONAL { ?book wdt:P31 ?instance. }
  OPTIONAL { ?book wdt:P136 ?genre. }
  ${late}
 }
GROUP BY ?book ?bookLabel ${moregroup}
LIMIT ${limit}`;
  const res = await fetch(`${SPARQL}?format=json&query=${encodeURIComponent(query)}`, { headers: { Accept: "application/sparql-results+json" } });
  if (res.status === 429) throw dalibre.error("rateLimited", "Wikidata asks to slow down. Try again in a minute.");
  // The original logs a failed query and goes on to the next one.
  if (!res.ok) {
    console.warn(`Wikidata answered ${res.status} to a query.`);
    return null;
  }
  let data;
  try {
    data = await res.json();
  } catch {
    return null;
  }
  const rows = data?.results?.bindings ?? [];
  return rows.length ? rows : null;
}

/**
 * download_cover: images (P18) of the matches, in one query rather than
 * one per book, as Dalibre wants the cover with the candidate.
 * @param {Dalibre.Candidate[]} candidates
 */
async function addCovers(candidates) {
  const ids = candidates.map((c) => c.identifiers?.wd).filter(Boolean);
  if (!ids.length) return;
  const query = `SELECT ?book (SAMPLE(?image) AS ?img) WHERE { VALUES ?book { ${ids.map((i) => `wd:${i}`).join(" ")} } ?book wdt:P18 ?image. } GROUP BY ?book`;
  try {
    const res = await fetch(`${SPARQL}?format=json&query=${encodeURIComponent(query)}`, { headers: { Accept: "application/sparql-results+json" } });
    if (!res.ok) return;
    const data = await res.json();
    /** @type {Map<string, string>} */
    const images = new Map();
    for (const r of data?.results?.bindings ?? []) {
      const id = /\/(Q\d+)$/.exec(r.book?.value ?? "")?.[1];
      if (id && r.img?.value) images.set(id, String(r.img.value).replace(/^http:/, "https:"));
    }
    for (const c of candidates) {
      const img = images.get(c.identifiers?.wd ?? "");
      if (img) c.cover = img;
    }
  } catch {
    // As the original: a failed cover query means no cover.
  }
}

/**
 * fake_book: a result that only saves the Gutenberg id.
 * @param {Prefs} p @param {string} title @param {string[]} authors @param {Record<string, string>} identifiers @param {string | null} gid
 * @returns {Dalibre.Candidate | null}
 */
function fakeBook(p, title, authors, identifiers, gid) {
  if (!(gid && p.gutensave && !identifiers.gutenberg && title)) return null;
  return { id: `gutenberg-${gid}`, title, authors, identifiers: { gutenberg: gid } };
}

/** A SPARQL string literal, optionally with a language tag. @param {string} s @param {string} [lang] */
function lit(s, lang) {
  const q = `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, " ")}"`;
  return lang ? `${q}@${lang.replace(/[^\w-]/g, "")}` : q;
}

/** @param {Set<any>} a @param {Set<any>} b */
function intersection(a, b) {
  let n = 0;
  for (const x of a) if (b.has(x)) n++;
  return n;
}

/** calibre's check_isbn: the digits of a valid ISBN, else "". @param {string} raw */
function checkISBN(raw) {
  const d = raw.replace(/[^\dXx]/g, "").toUpperCase();
  if (/^(\d)\1{9,12}$/.test(d)) return "";
  if (d.length === 10) {
    let sum = 0;
    for (let i = 0; i < 9; i++) sum += (i + 1) * Number(d[i]);
    const check = sum % 11;
    return (check === 10 ? "X" : String(check)) === d[9] ? d : "";
  }
  if (d.length === 13 && /^\d+$/.test(d)) {
    let sum = 0;
    for (let i = 0; i < 12; i++) sum += Number(d[i]) * (i % 2 ? 3 : 1);
    return (10 - (sum % 10)) % 10 === Number(d[12]) ? d : "";
  }
  return "";
}

/**
 * calibre's Source.get_title_tokens. (calibre's pattern puts 《》「」“”
 * after the character class by mistake; they're in it here.)
 * @param {string | undefined} title @param {boolean} stripSubtitle
 */
function* getTitleTokens(title, stripSubtitle) {
  if (!title) return;
  if (stripSubtitle) {
    const subtitle = /([([{].*?[)\]}]|[/:\\].*$)/g;
    if (title.replace(subtitle, "").length > 1) title = title.replace(subtitle, "");
  }
  /** @type {[RegExp, string][]} */
  const patterns = [
    [/[({[](\d{4}|omnibus|anthology|hardcover|audiobook|audio\scd|paperback|turtleback|mass\s*market|edition|ed\.)[\])}]/gi, ""],
    [/[({[].*?(edition|ed.).*?[\]})]/gi, ""],
    [/(\d+),(\d+)/gi, "$1$2"],
    [/(\s-)/gi, " "],
    [/[:,;!@$%^&*(){}.`~"\s[\]/《》「」“”]/gi, " "],
  ];
  for (const [re, repl] of patterns) title = title.replace(re, repl);
  for (let token of title.split(/\s+/)) {
    token = token.trim().replace(/^"+|"+$/g, "").replace(/^'+|'+$/g, "");
    if (token && !["a", "and", "the", "&"].includes(token.toLowerCase())) yield token;
  }
}
