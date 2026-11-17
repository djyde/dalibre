// @ts-check
// EPUB Split: ported from Jim Miller's EpubSplit calibre plugin
// (https://github.com/JimmXinu/EpubSplit, GPL-3.0): epubsplit.py
// (SplitEpub, FileCache, splitHtml) for the EPUB, epubsplit_plugin.py for
// the split modes and the new books' details.
//
// The book can be split at each spine file and each TOC entry with an
// anchor ("split lines"). The chosen lines become a new EPUB: the files,
// cut at the anchors, the images and CSS they use, and a TOC of the
// lines' entries.

import { escape, openEpub, strToU8, text } from "../shared/epub.js";
import { byName, elements, getAttr, getAttrLocal, local, parse, remove, serialize, setAttr, textOf } from "../shared/markup.js";
import { baseName, coverImage, deobfuscate, dirOf, encodePath, extOf, normpath, obfuscatedFonts, outFile, tocTree, unquote } from "../shared/epubkit.js";
import { readFile, writeFile } from "node:fs/promises";
import { zipSync } from "fflate";

/**
 * @typedef {{ href: string, anchor: string | null, toc: string[], id: string, type: string, num: number,
 *   guide?: [string, string], include?: boolean }} Line
 */

const MIME = /** @type {Record<string, string>} */ ({
  jpg: "image/jpeg", jpeg: "image/jpeg", png: "image/png", gif: "image/gif", svg: "image/svg+xml", webp: "image/webp",
  css: "text/css", ttf: "application/x-font-ttf", otf: "application/vnd.ms-opentype", woff: "application/font-woff", woff2: "font/woff2",
  xhtml: "application/xhtml+xml", html: "application/xhtml+xml", htm: "application/xhtml+xml",
});

const PER_SECTION = "per_section";
const PER_N_SECTIONS = "per_n_sections";
const PER_N_SPLITS = "per_n_splits";

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  if (action !== "split") throw dalibre.error("invalid", `Unknown action ${action}.`);
  const misource = input.books[0];
  const file = misource && input.files?.[misource.id];
  if (!file) throw dalibre.error("invalid", "Splitting needs a book with an EPUB.");
  const epub = await openEpub(file.path);
  const split = new SplitEpub(epub);
  const lines = split.getSplitLines();
  if (!lines.length) throw dalibre.error("invalid", "This EPUB has no sections to split off.");

  // EpubSplit's dialog: the sections, and its two buttons, one new book
  // or several as its "new book per" preference says (here, settings).
  const set = dalibre.settings;
  const per = String(set.new_book_per || PER_SECTION);
  const perTitle = { [PER_SECTION]: "New Book per Section", [PER_N_SECTIONS]: "New Book per N Sections", [PER_N_SPLITS]: "N New Books" }[per] ?? "New Book per Section";
  const a = await dalibre.form({
    submit: "Split",
    fields: [
      {
        key: "lines",
        title: "Sections",
        type: "multiselect",
        value: [],
        options: lines.slice(0, 500).map((l) => ({
          value: String(l.num),
          title: `${l.toc.join("|") || "—"} · ${l.href}${l.anchor ? `#${l.anchor}` : ""}${l.guide ? ` (${l.guide[0]}):${l.guide[1]}` : ""}`,
        })),
      },
      { key: "split", title: "Make", type: "select", value: "one", options: [{ value: "one", title: "One new book" }, { value: "splits", title: perTitle }] },
    ],
  });
  const picked = (Array.isArray(a.lines) ? a.lines : String(a.lines ?? "").split(","))
    .map((x) => parseInt(String(x), 10))
    .filter((n) => Number.isInteger(n) && n >= 0 && n < lines.length)
    .sort((x, y) => x - y);
  if (!picked.length) throw dalibre.error("invalid", "Book not split: select at least one section.");
  const mode = a.split === "splits" ? per : "one";
  const libCover = input.covers?.[misource.id];
  const cover = libCover ? { data: new Uint8Array(await readFile(libCover)), ext: extOf(libCover) || "jpg" } : null;

  /** @type {Dalibre.ActionResult["add"]} */
  const add = [];
  if (mode === "one") {
    add.push(await doSplit(split, epub, misource, picked, defTitle(lines, picked, misource), cover));
  } else {
    // _do_splits: lists of sections, one per new book. Each line is its own
    // list here; EpubSplit's dialog can also group lines.
    let linelists = picked.map((n) => [n]);
    /** @type {number[][]} */
    let dolists = [];
    if (mode === PER_N_SPLITS) {
      let splitN = Math.max(1, parseInt(String(set.n_splits_num || "3"), 10) || 3);
      while (linelists.length > 0) {
        const size = Math.ceil(linelists.length / splitN);
        dolists.push(linelists.slice(0, size).flat());
        linelists = linelists.slice(size);
        splitN = Math.max(1, splitN - 1);
      }
    } else if (mode === PER_N_SECTIONS) {
      const size = Math.max(1, parseInt(String(set.n_sections_num || "10"), 10) || 10);
      const orphans = Math.max(0, parseInt(String(set.orphans_num || "2"), 10) || 0);
      while (linelists.length > 0) {
        if (linelists.length - size > orphans) {
          dolists.push(linelists.slice(0, size).flat());
          linelists = linelists.slice(size);
        } else {
          dolists.push(linelists.flat());
          linelists = [];
        }
      }
    } else dolists = linelists;
    if (dolists.length > 200) throw dalibre.error("invalid", `That makes ${dolists.length} books; Dalibre adds at most 200 at a time.`);
    for (const [k, list] of dolists.entries()) {
      dalibre.progress(k, dolists.length);
      add.push(await doSplit(new SplitEpub(epub), epub, misource, list, defTitle(lines, list, misource), cover, k + 1));
    }
  }
  return { add, message: add.length === 1 ? `New book “${add[0].metadata?.title}” created.` : `${add.length} New Books Created.` };
}

/** An EpubSplit preference, from the settings. @param {string} k @param {boolean} def */
function pref(k, def) {
  const v = dalibre.settings[k];
  return v === undefined || v === "" ? def : v === "true";
}

/**
 * The default title, as _do_split: the first section's TOC entry
 * (copytoctitle), else "<title> Split" (copytitle).
 * @param {Line[]} lines @param {number[]} linenums @param {Dalibre.BookInfo} misource
 */
function defTitle(lines, linenums, misource) {
  if (pref("copytoctitle", true) && lines[linenums[0]].toc[0]) return lines[linenums[0]].toc[0];
  if (pref("copytitle", true)) return `${misource.title} Split`;
  return "";
}

/**
 * _do_split of epubsplit_plugin.py: the new book's details, copied from
 * the source as EpubSplit's preferences say, and the EPUB.
 * @param {SplitEpub} split @param {import("../shared/epub.js").Epub} epub @param {Dalibre.BookInfo} misource
 * @param {number[]} linenums @param {string} title @param {{ data: Uint8Array, ext: string } | null} libCover @param {number} [k]
 */
async function doSplit(split, epub, misource, linenums, title, libCover, k = 0) {
  /** @type {string[]} */
  const titles = [];
  if (pref("copycommentstitle", true)) titles.push(`<em>${escape(misource.title)}</em>`);
  if (pref("copycommentsidurl", true) && misource.identifiers?.url) titles.push(`<a href="${escape(misource.identifiers.url)}">Source URL</a>`);
  let comments = titles.length ? `<p>${titles.join("<br>")}</p>\n` : "";
  if (pref("copycomments", true) && misource.comments) comments += misource.comments;
  if (comments) comments = `<div><p>Split from:</p>\n${comments}</div>`;

  const authors = pref("copyauthors", true) ? misource.authors : [];
  const tags = pref("copytags", true) ? misource.tags ?? [] : [];
  const languages = pref("copylanguages", true) ? misource.languages ?? [] : [];
  // copycover: the source's library cover; without one (or in the CLI),
  // the EPUB's own cover image.
  let cover = null;
  if (pref("copycover", true)) {
    const own = coverImage(epub);
    cover = libCover ?? (own ? { data: epub.files[own.path], ext: extOf(own.path) || "jpg" } : null);
  }
  const data = split.writeSplitEpub(linenums, { authoropts: authors, titleopt: title, descopt: comments, tags, languages, cover });
  const path = await outFile(`split-${k}`, title || misource.title, "epub");
  await writeFile(path, data);

  /** @type {Dalibre.Changes} */
  const metadata = { title: title || `${split.origtitle} Split` };
  if (comments) metadata.comments = comments;
  if (authors.length) metadata.authors = authors;
  if (tags.length) metadata.tags = tags;
  if (languages.length) metadata.languages = languages;
  if (pref("copyseries", true) && misource.series) metadata.series = misource.series;
  if (pref("copyrating", true) && misource.rating !== undefined) metadata.rating = misource.rating;
  if (pref("copypubdate", true) && misource.pubdate) metadata.pubdate = misource.pubdate;
  if (pref("copypublisher", true) && misource.publisher) metadata.publisher = misource.publisher;
  if (pref("copyidentifiers", false) && misource.identifiers) metadata.identifiers = misource.identifiers;
  return { path, metadata };
}

/** SplitEpub of epubsplit.py. */
class SplitEpub {
  /** @param {import("../shared/epub.js").Epub} epub */
  constructor(epub) {
    this.epub = epub;
    this.relpath = epub.opfDir;
    this.dom = parse(epub.opf);
    /** @type {Map<string, [string, string]>} "h:"+href → [id, type]; "i:"+id → [href, type] */
    this.manifest = new Map();
    this.tocRelpath = "";
    /** @type {import("../shared/markup.js").Root | null} */
    this.tocDom = null;
    for (const item of byName(this.dom, "item")) {
      const fullhref = normpath(unquote(this.relpath + (getAttr(item, "href") ?? "")));
      const type = getAttr(item, "media-type") ?? "";
      this.manifest.set("h:" + fullhref, [getAttr(item, "id") ?? "", type]);
      this.manifest.set("i:" + (getAttr(item, "id") ?? ""), [fullhref, type]);
      if (type === "application/x-dtbncx+xml") {
        this.tocRelpath = dirOf(fullhref);
        this.tocDom = parse(text(epub, fullhref));
      }
    }
    /** @type {Map<string, [string, string]>} */
    this.guide = new Map();
    for (const ref of byName(this.dom, "reference")) {
      this.guide.set(normpath(unquote(this.relpath + (getAttr(ref, "href") ?? ""))), [getAttr(ref, "type") ?? "", getAttr(ref, "title") ?? ""]);
    }
    /** @type {Line[]} */
    this.lines = [];
    const t = byName(this.dom, "title")[0];
    this.origtitle = (t && textOf(t).trim()) || "(Title Missing)";
    /** @type {string[]} */
    this.origauthors = [];
    for (const c of byName(this.dom, "creator")) {
      const role = getAttrLocal(c, "role");
      const name = textOf(c).trim();
      if ((role === "aut" || role === undefined) && name && !this.origauthors.includes(name)) this.origauthors.push(name);
    }
    if (!this.origauthors.length) this.origauthors.push("(Authors Missing)");
  }

  /**
   * get_toc_map: href → [[text, anchor], …] from the NCX, file links ahead
   * of anchor links. Books without an NCX use their EPUB 3 nav (EpubSplit
   * only reads NCX files, and refuses EPUB 3 books).
   * @returns {Map<string, [string, string | null][]>}
   */
  tocMap() {
    /** @type {Map<string, [string, string | null][]>} */
    const map = new Map();
    /** @param {string} href @param {string | null} anchor @param {string} text */
    const put = (href, anchor, text) => {
      const list = map.get(href) ?? [];
      map.set(href, list);
      if (anchor === null) {
        let idx = 0;
        while (idx < list.length && list[idx][1] === null) idx++;
        list.splice(idx, 0, [text, anchor]);
      } else list.push([text, anchor]);
    };
    if (this.tocDom) {
      for (const np of byName(this.tocDom, "navpoint")) {
        const content = byName(np, "content")[0];
        const src = normpath(unquote(this.tocRelpath + (content ? getAttr(content, "src") ?? "" : "")));
        const [href, anchor] = src.includes("#") ? [src.slice(0, src.indexOf("#")), src.slice(src.indexOf("#") + 1)] : [src, null];
        const t = byName(np, "text")[0];
        put(href, anchor, t ? textOf(t).trim() : "");
      }
    } else {
      /** @param {import("../shared/epubkit.js").TocNode[]} nodes */
      const walk = (nodes) => {
        for (const n of nodes) {
          put(n.path, n.frag ? unquote(n.frag) : null, n.label);
          walk(n.children);
        }
      };
      walk(tocTree(this.epub));
    }
    return map;
  }

  /** get_split_lines: a line per spine file and per TOC entry with an anchor. */
  getSplitLines() {
    if (this.lines.length) return this.lines;
    const tocmap = this.tocMap();
    let count = 0;
    for (const itemref of byName(this.dom, "itemref")) {
      const idref = getAttr(itemref, "idref") ?? "";
      const entry = this.manifest.get("i:" + idref);
      if (!entry || !this.epub.files[entry[0]]) continue;
      const [href, type] = entry;
      /** @type {Line} */
      let current = { href, anchor: null, toc: [], id: idref, type, num: count++ };
      if (this.guide.has(href)) current.guide = this.guide.get(href);
      this.lines.push(current);
      for (const [text, anchor] of tocmap.get(href) ?? []) {
        if (anchor) {
          current = { href, anchor, toc: [], id: idref, type, num: count++ };
          this.lines.push(current);
        }
        current.toc.push(text);
      }
    }
    return this.lines;
  }

  /** get_split_files: the chunks of the chosen lines, cut at anchors, with links fixed. @param {number[]} linenums */
  getSplitFiles(linenums) {
    const cache = new FileCache(this.epub);
    this.cache = cache;
    const lines = this.getSplitLines();
    const set = new Set(linenums);
    lines.forEach((l, j) => (l.include = set.has(j)));

    /** @type {[string, Line, Line | null][]} */
    const outchunks = [];
    let inchunk = false;
    let currentfile = "";
    /** @type {Line | null} */
    let start = null;
    for (const line of lines) {
      if (line.include) {
        if (!inchunk) {
          inchunk = true;
          currentfile = line.href;
          start = line;
        } else if (currentfile !== line.href) {
          outchunks.push([currentfile, /** @type {Line} */ (start), line]);
          currentfile = line.href;
          start = line;
        }
      } else if (inchunk) {
        outchunks.push([currentfile, /** @type {Line} */ (start), line]);
        inchunk = false;
      }
    }
    if (inchunk) outchunks.push([currentfile, /** @type {Line} */ (start), null]);

    /** @type {[string, string, string, string][]} filename, id, type, data */
    const outfiles = [];
    for (const [href, s, end] of outchunks) {
      let filedata = text(this.epub, href);
      if (s.anchor !== null) filedata = splitHtml(filedata, s.anchor, false);
      if (end && end.anchor !== null && end.href === href) filedata = splitHtml(filedata, end.anchor, true);
      const filename = cache.addContentFile(href, filedata);
      outfiles.push([filename, s.id, s.type, filedata]);
    }

    // Internal links to anchors that moved.
    for (const fl of outfiles) {
      const doc = parse(fl[3], { html: true });
      let changed = false;
      for (const el of elements(doc)) {
        if (local(el) !== "a") continue;
        const h = getAttr(el, "href");
        if (h === undefined) continue;
        const path = normpath(unquote(dirOf(fl[0]) + h));
        const moved = cache.anchors.get(path);
        if (moved && moved !== path) {
          setAttr(el, "href", moved.slice(dirOf(fl[0]).length));
          changed = true;
        }
      }
      if (changed) fl[3] = serialize(doc);
    }
    return outfiles;
  }

  /**
   * write_split_epub.
   * @param {number[]} linenums
   * @param {{ authoropts: string[], titleopt: string, descopt: string, tags: string[], languages: string[],
   *   cover: { data: Uint8Array, ext: string } | null }} o
   */
  writeSplitEpub(linenums, o) {
    const files = this.getSplitFiles(linenums);
    const cache = /** @type {FileCache} */ (this.cache);
    /** @type {import("fflate").Zippable} */
    const out = { mimetype: [strToU8("application/epub+zip"), { level: 0 }] };
    out["META-INF/container.xml"] = strToU8(`<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
   <rootfiles>
      <rootfile full-path="content.opf" media-type="application/oebps-package+xml"/>
   </rootfiles>
</container>
`);
    const uniqueid = `epubsplit-uid-${Math.floor(Date.now() / 1000)}`;
    const meta = [`<dc:identifier id="epubsplit-id">${uniqueid}</dc:identifier>`, `<dc:title>${escape(o.titleopt || `${this.origtitle} Split`)}</dc:title>`];
    for (const author of new Set(o.authoropts.length ? o.authoropts : this.origauthors)) meta.push(`<dc:creator opf:role="aut">${escape(author)}</dc:creator>`);
    meta.push('<dc:contributor opf:role="bkp">epubsplit</dc:contributor>', "<dc:rights>Copyrights as per source stories</dc:rights>");
    for (const l of o.languages.length ? o.languages : ["en"]) meta.push(`<dc:language>${escape(l)}</dc:language>`);
    meta.push(`<dc:description>${escape(o.descopt || `Split from ${this.origtitle} by ${this.origauthors.join(", ")}.`)}</dc:description>`);
    for (const t of o.tags) meta.push(`<dc:subject>${escape(t)}</dc:subject>`);

    const manifest = ['<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>'];
    const spine = [];
    let guide = "";
    const cover = o.cover;
    let coverName = "";
    if (cover) {
      const ext = cover.ext;
      coverName = `cover.${ext}`;
      meta.push('<meta name="cover" content="coverimageid"/>');
      guide = '<guide>\n      <reference href="cover.xhtml" title="Cover" type="cover"/>\n   </guide>';
      manifest.push(`<item id="coverimageid" href="${coverName}" media-type="${MIME[ext] ?? "image/jpeg"}"/>`, '<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>');
      spine.push('<itemref idref="cover" linear="yes"/>');
    }

    let contentcount = 0;
    for (const [filename, , type, filedata] of files) {
      if (cover && filename === "cover.xhtml") continue;
      out[filename] = strToU8(filedata);
      const id = `a${contentcount++}`;
      manifest.push(`<item id="${id}" href="${escape(encodePath(filename))}" media-type="${escape(type)}"/>`);
      spine.push(`<itemref idref="${id}" linear="yes"/>`);
    }
    const fonts = obfuscatedFonts(this.epub);
    for (const linked of cache.linkedfiles) {
      if (cover && linked === coverName) continue;
      let data = this.epub.files[linked];
      if (!data) continue; // EpubSplit lists it anyway
      if (fonts.has(linked)) data = deobfuscate(this.epub, data, /** @type {string} */ (fonts.get(linked)));
      out[linked] = [data, { level: /\.(jpe?g|png|gif|webp|woff2?)$/i.test(linked) ? 0 : 6 }];
      // EpubSplit says "unknown" for files not in the manifest; a type from
      // the extension keeps the OPF valid.
      const mtype = this.manifest.get("h:" + linked)?.[1] ?? MIME[extOf(linked)] ?? "application/octet-stream";
      manifest.push(`<item id="a${contentcount++}" href="${escape(encodePath(linked))}" media-type="${escape(mtype)}"/>`);
    }
    if (cover) {
      out[coverName] = [cover.data, { level: 0 }];
      out["cover.xhtml"] = strToU8(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en"><head><title>Cover</title><style type="text/css" title="override_css">
@page {padding: 0pt; margin:0pt}
body { text-align: center; padding:0pt; margin: 0pt; }
div { margin: 0pt; padding: 0pt; }
</style></head><body><div>
<img src="${coverName}" alt="cover"/>
</div></body></html>
`);
    }

    out["content.opf"] = strToU8(`<?xml version="1.0" encoding="utf-8"?>
<package version="2.0" xmlns="http://www.idpf.org/2007/opf" unique-identifier="epubsplit-id">
   <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
      ${meta.join("\n      ")}
   </metadata>
   <manifest>
      ${manifest.join("\n      ")}
   </manifest>
   <spine toc="ncx">
      ${spine.join("\n      ")}
   </spine>
   ${guide}
</package>
`);

    // toc.ncx: one entry per TOC text of each included line.
    const points = [];
    let count = 1;
    for (const line of this.lines) {
      if (!line.include) continue;
      for (const t of line.toc) {
        const anchored = line.anchor ? cache.anchors.get(`${line.href}#${line.anchor}`) : undefined;
        const src = anchored ?? line.href;
        const hash = src.indexOf("#");
        const enc = hash < 0 ? encodePath(src) : encodePath(src.slice(0, hash)) + src.slice(hash);
        points.push(
          `      <navPoint id="a${String(count).padStart(3, "0")}" playOrder="${count}">\n         <navLabel>\n            <text>${escape(t)}</text>\n         </navLabel>\n         <content src="${escape(enc)}"/>\n      </navPoint>\n`,
        );
        count++;
      }
    }
    out["toc.ncx"] = strToU8(`<?xml version="1.0" encoding="utf-8"?>
<ncx version="2005-1" xmlns="http://www.daisy.org/z3986/2005/ncx/">
   <head>
      <meta content="${uniqueid}" name="dtb:uid"/>
      <meta content="1" name="dtb:depth"/>
      <meta content="0" name="dtb:totalPageCount"/>
      <meta content="0" name="dtb:maxPageNumber"/>
   </head>
   <docTitle>
      <text>${escape(o.titleopt)}</text>
   </docTitle>
   <navMap>
${points.join("")}   </navMap>
</ncx>
`);
    return zipSync(out);
  }
}

/** FileCache of epubsplit.py: new names for content files, their anchors and linked files. */
class FileCache {
  /** @param {import("../shared/epub.js").Epub} epub */
  constructor(epub) {
    this.epub = epub;
    /** @type {Map<string, string[]>} */
    this.oldnew = new Map();
    /** @type {Map<string, string>} */
    this.anchors = new Map();
    /** @type {Set<string>} */
    this.linkedfiles = new Set();
  }

  /** @param {string} href */
  fixPath(href) {
    return normpath(unquote(href));
  }

  /** @param {string} href */
  addLinkedFile(href) {
    this.linkedfiles.add(this.fixPath(href));
  }

  /** @param {string} href @param {string} filedata */
  addContentFile(href, filedata) {
    let newfile = href;
    const list = this.oldnew.get(href);
    if (!list) this.oldnew.set(href, []);
    else newfile = `${dirOf(href)}${list.length}-${baseName(href)}`;
    /** @type {string[]} */ (this.oldnew.get(href)).push(newfile);

    const doc = parse(filedata, { html: true });
    const cssdata = [];
    for (const el of elements(doc)) {
      const id = getAttr(el, "id");
      if (id !== undefined) this.anchors.set(`${href}#${id}`, `${newfile}#${id}`);
      const name = local(el);
      if (name === "img" || name === "image") {
        const src = getAttr(el, "xlink:href") ?? getAttr(el, "src");
        if (src) this.addLinkedFile(dirOf(href) + src);
      }
      if (name === "link" && getAttr(el, "type") === "text/css" && getAttr(el, "href")) this.addCssFile(dirOf(href) + getAttr(el, "href"));
      // EpubSplit only follows <link type="text/css">; rel="stylesheet"
      // without a type is followed too, or the book loses its styles.
      else if (name === "link" && /\bstylesheet\b/i.test(getAttr(el, "rel") ?? "") && getAttr(el, "href")) this.addCssFile(dirOf(href) + getAttr(el, "href"));
      if (name === "style") cssdata.push(textOf(el));
      const style = getAttr(el, "style");
      if (style) cssdata.push(style);
    }
    this.addCssData(href, cssdata.join("\n"));
    return newfile;
  }

  /** @param {string} href */
  addCssFile(href) {
    href = this.fixPath(href);
    if (this.linkedfiles.has(href)) return;
    this.linkedfiles.add(href);
    this.addCssData(href, text(this.epub, href));
  }

  /** @param {string} href @param {string} cssdata */
  addCssData(href, cssdata) {
    cssdata = cssdata.replace(/\/\*[\s\S]*?\*\//g, "");
    if (cssdata.includes("@import")) {
      for (const m of cssdata.matchAll(/@import (?:url\()?['"]?([^'")]+)['"]?\)?/g)) this.addCssFile(dirOf(href) + m[1]);
    }
    if (cssdata.includes("url(")) {
      for (const m of cssdata.matchAll(/url\(['"]?(.*?)['"]?\)/g)) this.addLinkedFile(dirOf(href) + m[1]);
    }
  }
}

/**
 * splitHtml: the document from the element with this id on, or (before)
 * up to it, dropping siblings up to the body.
 * @param {string} data @param {string} tagid @param {boolean} before
 */
export function splitHtml(data, tagid, before) {
  const doc = parse(data, { html: true });
  const splitpoint = elements(doc).find((e) => getAttr(e, "id") === tagid);
  if (!splitpoint) return data;
  // EpubSplit (BeautifulSoup) drops sibling elements only; text beside
  // them is dropped too unless it is whitespace, so no stray text remains.
  /** @param {import("../shared/markup.js").Node} n */
  const drop = (n) => n.type === "element" || (n.type === "text" && /\S/.test(textOf(n)));
  /** @param {import("../shared/markup.js").Node} node */
  const siblings = (node) => {
    const p = node.parent;
    if (!p) return [];
    const i = p.children.indexOf(node);
    return (before ? p.children.slice(i + 1) : p.children.slice(0, i)).filter(drop);
  };
  for (const n of siblings(splitpoint)) remove(n);
  let parent = splitpoint.parent;
  while (parent && parent.type === "element" && local(parent) !== "body") {
    for (const n of siblings(parent)) remove(n);
    parent = parent.parent;
  }
  if (before) remove(splitpoint);
  return serialize(doc);
}

