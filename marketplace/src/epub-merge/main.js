// @ts-check
// EPUB Merge: ported from Jim Miller's EpubMerge calibre plugin
// (https://github.com/JimmXinu/EpubMerge, GPL-3.0): epubmerge/epubmerge.py
// doMerge() for the EPUB, epubmerge_plugin.py for the new book's details.
//
// Each book's files go, unchanged, under a numbered folder (1/, 2/…) so
// their links keep working; a new content.opf and toc.ncx at the root tie
// them together. The TOC is built from each book's NCX.

import { escape, openEpub, strToU8, text } from "../shared/epub.js";
import { byName, childElements, getAttr, getAttrLocal, local, parse, textOf } from "../shared/markup.js";
import { coverImage, deobfuscate, dirOf, encodePath, extOf, normpath, obfuscatedFonts, outFile, tocTree, unquote } from "../shared/epubkit.js";
import { readFile, writeFile } from "node:fs/promises";
import { zipSync } from "fflate";

/** @typedef {{ id: string, label: string, src: string, children: NavPoint[] }} NavPoint */

const IMAGE_TYPES = /** @type {Record<string, string>} */ ({ jpg: "image/jpeg", jpeg: "image/jpeg", png: "image/png", gif: "image/gif", svg: "image/svg+xml" });

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  if (action !== "merge") throw dalibre.error("invalid", `Unknown action ${action}.`);
  const books = input.books.filter((b) => input.files?.[b.id]);
  if (books.length < 2) throw dalibre.error("invalid", "Merging needs at least two books with an EPUB.");

  // epubmerge_plugin.py: the default title is the series when every book
  // is in the same one, else "<first title> Anthology".
  const mergeword = (dalibre.settings.mergeword ?? "").trim() || "Anthology";
  const series = books.map((b) => b.series ?? "");
  const deftitle = series.every((s) => s && s === series[0]) ? series[0] : `${books[0].title} ${mergeword}`;

  // EpubMerge keeps its options in the plugin's preferences: here, settings.
  const set = dalibre.settings;
  const on = (/** @type {string} */ k, /** @type {boolean} */ def) => (set[k] === undefined || set[k] === "" ? def : set[k] === "true");
  const a = await dalibre.form({ submit: "Merge", fields: [{ key: "title", title: "Title", type: "text", value: deftitle }] });
  const title = String(a.title ?? "").trim() || deftitle;
  const total = books.length * 2 + 1;

  /** @type {import("../shared/epub.js").Epub[]} */
  const epubs = [];
  for (const [n, book] of books.entries()) {
    dalibre.progress(n, total);
    epubs.push(await openEpub(/** @type {Dalibre.HookFile} */ (input.files?.[book.id]).path));
  }

  // The new book's details, as epubmerge_plugin.py fills them.
  /** @type {string[]} */
  const authors = [];
  for (const [n, b] of books.entries()) {
    for (const x of b.authors.length ? b.authors : opfAuthors(epubs[n].opf).map((y) => y.name)) if (!authors.includes(x)) authors.push(x);
  }
  const publishers = new Set(books.map((b) => b.publisher ?? ""));
  const tags = unique([...books.flatMap((b) => b.tags ?? []), ...String(set.mergetags ?? "").split(",").map((t) => t.trim())]);
  const languages = unique(books.flatMap((b) => b.languages ?? []));
  /** @param {Dalibre.BookInfo} b */
  const booktitle = (b) => (authors.length > 1 ? `${b.title} by ${b.authors.join(" & ")}` : b.title);
  let comments = `<p>${mergeword} containing:</p>`;
  if (on("includecomments", false)) {
    comments +=
      '<div class="mergedbook">' +
      books.map((b) => (b.comments ? `<p><b>${escape(booktitle(b))}</b></p>${b.comments}` : `<b>${escape(booktitle(b))}</b><br/>`)).join('<hr></div><div class="mergedbook">') +
      "</div>";
  } else comments += books.map((b) => escape(booktitle(b))).join("<br/>");

  // The merged book gets the first book's library cover, as in EpubMerge.
  const libCover = input.covers?.[books[0].id];
  const coverjpg = libCover ? { data: new Uint8Array(await readFile(libCover)), ext: extOf(libCover) || "jpg" } : null;
  const data = doMerge(epubs, {
    cover: coverjpg,
    authoropts: authors,
    titleopt: title,
    descopt: comments,
    tags,
    languages: languages.length ? languages : ["en"],
    titlenavpoints: on("titlenavpoints", true),
    originalnavpoints: on("originalnavpoints", true),
    keepsingletocs: !on("removesingletocs", true),
    flattentoc: on("flattentoc", false),
    progress: (i) => dalibre.progress(books.length + i + 1, total),
  });

  const path = await outFile("merged", title, "epub");
  await writeFile(path, data);
  dalibre.progress(total, total);

  /** @type {Dalibre.Changes} */
  const metadata = { title, authors, tags, comments };
  if (languages.length) metadata.languages = languages;
  if (publishers.size === 1 && books[0].publisher) metadata.publisher = books[0].publisher;
  if (on("firstseries", false) && books[0].series) {
    metadata.series = books[0].series;
    if (books[0].seriesIndex !== undefined) metadata.seriesIndex = books[0].seriesIndex;
  }
  return { add: [{ path, metadata }], message: `Merged ${books.length} books into “${title}”.` };
}

/**
 * doMerge() of epubmerge.py.
 * @param {import("../shared/epub.js").Epub[]} epubs
 * @param {{ authoropts: string[], titleopt: string, descopt: string, tags: string[], languages: string[],
 *   titlenavpoints: boolean, originalnavpoints: boolean, keepsingletocs: boolean, flattentoc: boolean,
 *   cover: { data: Uint8Array, ext: string } | null, progress: (i: number) => void }} o
 */
function doMerge(epubs, o) {
  /** @type {import("fflate").Zippable} */
  const out = { mimetype: [strToU8("application/epub+zip"), { level: 0 }] };
  out["META-INF/container.xml"] = strToU8(`<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
   <rootfiles>
      <rootfile full-path="content.opf" media-type="application/oebps-package+xml"/>
   </rootfiles>
</container>
`);

  /** @type {[string, string, string][]} */
  const items = [["ncx", "toc.ncx", "application/x-dtbncx+xml"]];
  /** @type {string[]} */
  const itemrefs = [];
  /** @type {(NavPoint[] | null)[]} */
  const navmaps = [];
  /** @type {boolean[]} */
  const isFff = [];
  /** @type {string[]} */
  const firstitemhrefs = [];
  /** @type {string[]} */
  const booktitles = [];
  /** @type {string[][]} */
  const allauthors = [];
  /** @type {Record<string, string>} */
  const fileasauthors = {};
  const filelist = new Set();
  const outputpagedirs = new Set();
  let source = "";

  for (const [i, epub] of epubs.entries()) {
    const booknum = i + 1;
    const bookdir = `${booknum}/`;
    const bookid = `a${booknum}`;
    const relpath = epub.opfDir;
    const opf = parse(epub.opf);
    const fonts = obfuscatedFonts(epub);

    if (booknum === 1) {
      const s = byName(opf, "source")[0];
      source = s ? textOf(s).trim() : "";
    }

    isFff.push(
      [...byName(opf, "contributor"), ...byName(opf, "identifier")].some((c) => {
        const t = textOf(c).trim();
        return (
          t === "fanficdownloader [http://fanficdownloader.googlecode.com]" ||
          t === "FanFicFare [https://github.com/JimmXinu/FanFicFare]" ||
          (getAttrLocal(c, "scheme") ?? "").toLowerCase().includes("fanficfare-uid") ||
          (getAttr(c, "id") ?? "").toLowerCase().includes("fanficfare-uid")
        );
      }),
    );

    const titleEl = byName(opf, "title")[0];
    booktitles.push((titleEl && textOf(titleEl).trim()) || "(Title Missing)");
    const authors = opfAuthors(epub.opf);
    for (const x of authors) if (x.fileAs) fileasauthors[x.name] = x.fileAs;
    allauthors.push(authors.length ? authors.map((x) => x.name) : ["(Author Missing)"]);

    const spineEl = byName(opf, "spine")[0];
    outputpagedirs.add(spineEl ? getAttr(spineEl, "page-progression-direction") ?? "" : "");

    /** @type {Record<string, string>} */
    const itemhrefs = {};
    /** @type {NavPoint[] | null} */
    let navmap = null;
    const manifestEl = byName(opf, "manifest")[0];
    for (const item of manifestEl ? byName(manifestEl, "item") : []) {
      const id = getAttr(item, "id") ?? "";
      const itemhref = normpath(unquote(getAttr(item, "href") ?? ""));
      const href = normpath(bookdir + relpath + itemhref);
      const type = getAttr(item, "media-type") ?? "";
      const zipPath = normpath(relpath + itemhref);
      if (type === "application/x-dtbncx+xml") {
        // The book's TOC, with ids and paths made unique; the last NCX
        // wins, as in calibre's viewer.
        const ncx = text(epub, zipPath);
        if (ncx) navmap = ncxNavMap(parse(ncx), bookid, bookdir, zipPath) ?? navmap;
        continue;
      }
      itemhrefs[bookid + id] = href;
      if (filelist.has(href)) continue;
      let filedata = epub.files[zipPath];
      if (!filedata) {
        delete itemhrefs[bookid + id];
        continue;
      }
      if (fonts.has(zipPath)) filedata = deobfuscate(epub, filedata, /** @type {string} */ (fonts.get(zipPath)));
      out[href] = [filedata, { level: /\.(jpe?g|png|gif|webp|woff2?|mp3|mp4)$/i.test(href) ? 0 : 6 }];
      items.push([bookid + id, href, type]);
      filelist.add(href);
    }
    // EPUB 3 books without an NCX: their nav document instead (EpubMerge
    // reads only NCX files).
    if (!navmap && epub.manifest.some((m) => /\bnav\b/.test(m.props))) {
      let n = 0;
      /** @param {import("../shared/epubkit.js").TocNode[]} nodes @returns {NavPoint[]} */
      const conv = (nodes) => nodes.map((t) => ({ id: `${bookid}nav${++n}`, label: t.label, src: encodePath(bookdir + t.path) + (t.frag ? `#${t.frag}` : ""), children: conv(t.children) }));
      const tree = conv(tocTree(epub));
      navmap = tree.length ? tree : null;
    }
    navmaps.push(navmap);

    const refs = spineEl ? byName(spineEl, "itemref").map((r) => bookid + (getAttr(r, "idref") ?? "")) : [];
    const first = refs.find((r) => r in itemhrefs);
    firstitemhrefs.push(first ? encodePath(itemhrefs[first]) : "");
    // Only spine entries whose file is there; EpubMerge keeps them all.
    for (const r of refs) if (r in itemhrefs) itemrefs.push(r);
    o.progress(i);
  }

  // content.opf
  const uniqueid = `epubmerge-uid-${Math.floor(Date.now() / 1000)}`;
  const useauthors = o.authoropts.length > 1 ? [o.authoropts] : allauthors;
  const usedauthors = new Set();
  const meta = [`<dc:identifier id="epubmerge-id">${uniqueid}</dc:identifier>`, `<dc:title>${escape(o.titleopt)}</dc:title>`];
  for (const list of useauthors) {
    for (const author of list) {
      if (usedauthors.has(author)) continue;
      usedauthors.add(author);
      const fileas = o.authoropts.length > 1 ? "" : fileasauthors[author];
      meta.push(`<dc:creator opf:role="aut"${fileas ? ` opf:file-as="${escape(fileas)}"` : ""}>${escape(author)}</dc:creator>`);
    }
  }
  meta.push("<dc:contributor>epubmerge</dc:contributor>", "<dc:rights>Copyrights as per source stories</dc:rights>");
  for (const l of o.languages) meta.push(`<dc:language>${escape(l)}</dc:language>`);
  meta.push(`<dc:description>${escape(o.descopt)}</dc:description>`);
  if (source) meta.push(`<dc:identifier opf:scheme="URL">${escape(source)}</dc:identifier>`, `<dc:source>${escape(source)}</dc:source>`);
  for (const t of o.tags) meta.push(`<dc:subject>${escape(t)}</dc:subject>`);

  const manifest = [];
  const spine = [];
  let guide = "";
  // EpubMerge's coverjpgpath: the library cover; for a book without one
  // (or the CLI), the first book's own cover image.
  const own = coverImage(epubs[0]);
  const cover = o.cover ?? (own ? { data: epubs[0].files[own.path], ext: extOf(own.path) || "jpg" } : null);
  if (cover) {
    const coverext = cover.ext;
    const covertype = IMAGE_TYPES[coverext] ?? "image/jpeg";
    meta.push('<meta name="cover" content="coverimageid"/>');
    guide = `<guide>\n      <reference href="cover.xhtml" title="Cover" type="cover"/>\n   </guide>`;
    manifest.push(`<item id="coverimageid" href="cover.${coverext}" media-type="${covertype}"/>`, `<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>`);
    spine.push('<itemref idref="cover" linear="yes"/>');
    out[`cover.${coverext}`] = [cover.data, { level: 0 }];
    out["cover.xhtml"] = strToU8(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en"><head><title>Cover</title><style type="text/css" title="override_css">
@page {padding: 0pt; margin:0pt}
body { text-align: center; padding:0pt; margin: 0pt; }
div { margin: 0pt; padding: 0pt; }
</style></head><body><div>
<img src="cover.${coverext}" alt="cover"/>
</div></body></html>
`);
  }
  for (const [id, href, type] of items) manifest.push(`<item id="${escape(id)}" href="${escape(encodePath(href))}" media-type="${escape(type)}"/>`);
  for (const r of itemrefs) spine.push(`<itemref idref="${escape(r)}" linear="yes"/>`);
  const pagedir = outputpagedirs.size === 1 ? [...outputpagedirs][0] : "";

  out["content.opf"] = strToU8(`<?xml version="1.0" encoding="utf-8"?>
<package version="2.0" xmlns="http://www.idpf.org/2007/opf" unique-identifier="epubmerge-id">
   <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
      ${meta.join("\n      ")}
   </metadata>
   <manifest>
      ${manifest.join("\n      ")}
   </manifest>
   <spine toc="ncx"${pagedir ? ` page-progression-direction="${escape(pagedir)}"` : ""}>
      ${spine.join("\n      ")}
   </spine>
   ${guide}
</package>
`);

  // toc.ncx
  /** @type {NavPoint[]} */
  let tocnavMap = [];
  for (const [booknum, navmap] of navmaps.entries()) {
    const navpoints = navmap ?? [];
    /** @type {NavPoint[]} */
    let target = tocnavMap;
    if (o.titlenavpoints) {
      // For the TOC, the first author; left out when there's only one.
      const label = usedauthors.size > 1 ? `${booktitles[booknum]} by ${allauthors[booknum][0]}` : booktitles[booknum];
      const newnav = { id: `book${String(booknum).padStart(3, "0")}`, label, src: firstitemhrefs[booknum], children: [] };
      tocnavMap.push(newnav);
      target = newnav.children;
    }
    // EpubMerge copies a book's TOC only under its title entry; with title
    // entries off that would leave the TOC empty, so the entries go at the
    // top level then.
    const depthnavpoints = count(navpoints);
    if (o.originalnavpoints && (o.keepsingletocs || depthnavpoints > 1)) {
      let nonskip = depthnavpoints;
      if (isFff[booknum]) {
        // FanFicFare books: don't count their log and title pages.
        nonskip = 0;
        for (const np of navpoints) {
          const skip = np.id.endsWith("log_page") || np.src.endsWith("log_page.xhtml") || np.id.endsWith("title_page") || np.src.endsWith("title_page.xhtml");
          if (!skip) nonskip += 1 + count(np.children);
        }
      }
      if (o.keepsingletocs || nonskip > 1) target.push(...navpoints);
    }
  }

  // Strict playOrder: entries pointing at the same src share one.
  const contentsrcs = new Set();
  let playorder = 0;
  let maxdepth = 0;
  /** @type {Map<NavPoint, number>} */
  const orders = new Map();
  /** @param {NavPoint[]} nps @param {number} depth */
  const number = (nps, depth) => {
    for (const np of nps) {
      if (!contentsrcs.has(np.src)) {
        contentsrcs.add(np.src);
        playorder++;
        maxdepth = Math.max(maxdepth, depth);
      }
      orders.set(np, playorder);
      number(np.children, depth + 1);
    }
  };
  number(tocnavMap, 1);
  if (o.flattentoc) {
    maxdepth = 1;
    /** @param {NavPoint[]} nps @returns {NavPoint[]} */
    const flat = (nps) => nps.flatMap((np) => [np, ...flat(np.children)]);
    const all = flat(tocnavMap);
    tocnavMap = all.map((np) => ({ ...np, children: [] }));
    all.forEach((np, k) => orders.set(tocnavMap[k], /** @type {number} */ (orders.get(np))));
  }
  /** @param {NavPoint[]} nps @param {string} ind @returns {string} */
  const points = (nps, ind) =>
    nps
      .map(
        (np) =>
          `${ind}<navPoint id="${escape(np.id)}" playOrder="${orders.get(np)}">\n${ind}   <navLabel>\n${ind}      <text>${escape(np.label)}</text>\n${ind}   </navLabel>\n${ind}   <content src="${escape(np.src)}"/>\n${points(np.children, ind + "   ")}${ind}</navPoint>\n`,
      )
      .join("");
  out["toc.ncx"] = strToU8(`<?xml version="1.0" encoding="utf-8"?>
<ncx version="2005-1" xmlns="http://www.daisy.org/z3986/2005/ncx/">
   <head>
      <meta content="${uniqueid}" name="dtb:uid"/>
      <meta content="${Math.max(maxdepth, 1)}" name="dtb:depth"/>
      <meta content="0" name="dtb:totalPageCount"/>
      <meta content="0" name="dtb:maxPageNumber"/>
   </head>
   <docTitle>
      <text>${escape(o.titleopt)}</text>
   </docTitle>
   <navMap>
${points(tocnavMap, "      ")}   </navMap>
</ncx>
`);
  return zipSync(out);
}

/**
 * The navPoints of an NCX's navMap, ids prefixed by bookid, each src moved
 * under the book's folder. (EpubMerge resolves srcs against the OPF's
 * folder; here against the NCX's, where they belong.)
 * @param {import("../shared/markup.js").Root} ncx @param {string} bookid @param {string} bookdir @param {string} ncxPath
 * @returns {NavPoint[] | null}
 */
function ncxNavMap(ncx, bookid, bookdir, ncxPath) {
  const navMap = byName(ncx, "navmap")[0];
  if (!navMap) return null;
  const ncxDir = dirOf(ncxPath);
  /** @param {import("../shared/markup.js").Element} parent @returns {NavPoint[]} */
  const conv = (parent) =>
    childElements(parent)
      .filter((e) => local(e) === "navpoint")
      .map((np) => {
        const label = childElements(np).find((e) => local(e) === "navlabel");
        const content = childElements(np).find((e) => local(e) === "content");
        const src = content ? getAttr(content, "src") ?? "" : "";
        const hash = src.indexOf("#");
        const file = hash < 0 ? src : src.slice(0, hash);
        const frag = hash < 0 ? "" : src.slice(hash);
        return {
          id: bookid + (getAttr(np, "id") ?? ""),
          label: label ? textOf(label).replace(/\s+/g, " ").trim() : "",
          src: encodePath(normpath(bookdir + ncxDir + unquote(file))) + frag,
          children: conv(np),
        };
      });
  return conv(navMap);
}

/** @param {NavPoint[]} nps @returns {number} */
function count(nps) {
  return nps.reduce((n, np) => n + 1 + count(np.children), 0);
}

/** An OPF's authors, as EpubMerge reads them: dc:creator with role aut or none. @param {string} opf */
function opfAuthors(opf) {
  return byName(parse(opf), "creator")
    .filter((c) => {
      const role = getAttrLocal(c, "role");
      return role === "aut" || role === undefined;
    })
    .map((c) => ({ name: textOf(c).trim(), fileAs: getAttrLocal(c, "file-as") ?? "" }))
    .filter((x) => x.name);
}

/** @param {string[]} list */
function unique(list) {
  return [...new Set(list.filter(Boolean))];
}
