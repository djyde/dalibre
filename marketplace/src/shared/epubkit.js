// @ts-check
// Building blocks for plugins that make new EPUBs from old ones: the table
// of contents as a tree, paths and hrefs, covers, font obfuscation, and
// output files in /work.

import { attr, docTitle, resolve, spineDocs, text, unescape } from "./epub.js";
import { tokenize } from "./markup.js";
import { mkdir } from "node:fs/promises";

/**
 * An entry of a table of contents. path is the target's path in the zip.
 * @typedef {{ label: string, path: string, frag: string, children: TocNode[] }} TocNode
 */

/** @param {string} s */
const clean = (s) => unescape(s.replace(/<[^>]+>/g, "")).replace(/\s+/g, " ").trim();

/**
 * The book's table of contents, nested: the EPUB 3 nav, else the NCX, else
 * one entry per spine document.
 * @param {import("./epub.js").Epub} epub @returns {TocNode[]}
 */
export function tocTree(epub) {
  const nav = epub.manifest.find((i) => /\bnav\b/.test(i.props));
  if (nav) {
    const html = text(epub, nav.path);
    const block = /<nav\b[^>]*epub:type\s*=\s*["'][^"']*\btoc\b[^"']*["'][^>]*>([\s\S]*?)<\/nav>/i.exec(html)?.[1];
    const tree = block ? navTree(block, dirOf(nav.path)) : [];
    if (tree.length) return tree;
  }
  const ncx = epub.manifest.find((i) => i.type === "application/x-dtbncx+xml");
  if (ncx) {
    const tree = ncxTree(text(epub, ncx.path), dirOf(ncx.path));
    if (tree.length) return tree;
  }
  return spineDocs(epub)
    .filter((s) => s.linear)
    .map((s) => ({ label: docTitle(text(epub, s.item.path)) || baseName(s.item.path), path: s.item.path, frag: "", children: [] }));
}

/** @param {string} html @param {string} dir */
function navTree(html, dir) {
  /** @type {TocNode[]} */
  const root = [];
  /** @type {TocNode[]} */
  const open = [];
  let capture = "";
  let buf = "";
  for (const t of tokenize(html)) {
    const name = t.name.toLowerCase();
    if (t.kind === "open" && name === "li" && !t.self) {
      const node = { label: "", path: "", frag: "", children: [] };
      (open.length ? open[open.length - 1].children : root).push(node);
      open.push(node);
    } else if (t.kind === "close" && name === "li") {
      open.pop();
    } else if (t.kind === "open" && (name === "a" || name === "span") && !t.self && open.length && !capture && !open[open.length - 1].label) {
      capture = name;
      buf = "";
      const href = name === "a" ? attr(t.raw, "href") : "";
      if (href) {
        const node = open[open.length - 1];
        node.path = resolve(dir, href);
        node.frag = href.split("#")[1] ?? "";
      }
    } else if (t.kind === "close" && name === capture) {
      open[open.length - 1].label = clean(buf);
      capture = "";
    } else if (capture && t.kind === "text") buf += t.raw;
  }
  return prune(root);
}

/** @param {string} xml @param {string} dir */
function ncxTree(xml, dir) {
  /** @type {TocNode[]} */
  const root = [];
  /** @type {TocNode[]} */
  const open = [];
  let inText = false;
  let buf = "";
  for (const t of tokenize(xml)) {
    const name = t.name.toLowerCase();
    if (t.kind === "open" && name === "navpoint" && !t.self) {
      const node = { label: "", path: "", frag: "", children: [] };
      (open.length ? open[open.length - 1].children : root).push(node);
      open.push(node);
    } else if (t.kind === "close" && name === "navpoint") open.pop();
    else if (t.kind === "open" && name === "text" && open.length && !open[open.length - 1].label) {
      inText = true;
      buf = "";
    } else if (t.kind === "close" && name === "text" && inText) {
      open[open.length - 1].label = clean(buf);
      inText = false;
    } else if (inText && t.kind === "text") buf += t.raw;
    else if (t.kind === "open" && name === "content" && open.length && !open[open.length - 1].path) {
      const src = attr(t.raw, "src");
      if (src) {
        open[open.length - 1].path = resolve(dir, src);
        open[open.length - 1].frag = src.split("#")[1] ?? "";
      }
    }
  }
  return prune(root);
}

/** Entries without a target take their first child's; empty ones go. @param {TocNode[]} nodes @returns {TocNode[]} */
function prune(nodes) {
  /** @type {TocNode[]} */
  const out = [];
  for (const n of nodes) {
    n.children = prune(n.children);
    if (!n.path && n.children.length) {
      n.path = n.children[0].path;
      n.frag = n.children[0].frag;
    }
    if (!n.path) continue;
    if (!n.label) n.label = baseName(n.path);
    out.push(n);
  }
  return out;
}

/** A zip path as an href: each part percent-encoded. @param {string} path */
export function encodePath(path) {
  return path
    .split("/")
    .map((p) => (p === ".." ? p : encodeURIComponent(p).replace(/%2C/g, ",").replace(/%40/g, "@")))
    .join("/");
}

/** @param {string} path */
export function dirOf(path) {
  return path.slice(0, path.lastIndexOf("/") + 1);
}

/** @param {string} path */
export function baseName(path) {
  return path.slice(path.lastIndexOf("/") + 1);
}

/** Writes a file into /work under its own folder, so books with one title don't collide. */
export async function outFile(/** @type {string} */ dir, /** @type {string} */ title, /** @type {string} */ ext) {
  const base = title.replace(/[\\/:*?"<>|\x00-\x1f]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 80) || "book";
  const folder = `/work/${dir}`;
  await mkdir(folder, { recursive: true });
  return `${folder}/${base}.${ext}`;
}

// ---- Font obfuscation (IDPF and Adobe), as EpubMerge's and EpubSplit's FontDecrypter ----

const ADOBE_OBFUSCATION = "http://ns.adobe.com/pdf/enc#RC";
const IDPF_OBFUSCATION = "http://www.idpf.org/2008/embedding";

/**
 * The obfuscated fonts of the book, by path, with their algorithm. Other
 * encryption (DRM) isn't listed.
 * @param {import("./epub.js").Epub} epub @returns {Map<string, string>}
 */
export function obfuscatedFonts(epub) {
  const out = new Map();
  const xml = text(epub, "META-INF/encryption.xml");
  for (const m of xml.matchAll(/<(?:\w+:)?EncryptedData\b[\s\S]*?<\/(?:\w+:)?EncryptedData>/g)) {
    const alg = attr(/<(?:\w+:)?EncryptionMethod\b[^>]*>/.exec(m[0])?.[0] ?? "", "Algorithm");
    if (alg !== ADOBE_OBFUSCATION && alg !== IDPF_OBFUSCATION) continue;
    for (const r of m[0].matchAll(/<(?:\w+:)?CipherReference\b[^>]*>/g)) out.set(attr(r[0], "URI"), alg);
  }
  return out;
}

/**
 * A font's data with its obfuscation undone, as FontDecrypter does: the
 * key is the book's unique identifier, SHA-1'd for IDPF, as UUID bytes for
 * Adobe.
 * @param {import("./epub.js").Epub} epub @param {Uint8Array} data @param {string} algorithm
 */
export function deobfuscate(epub, data, algorithm) {
  const uidKey = attr(/<(?:\w+:)?package\b[^>]*>/.exec(epub.opf)?.[0] ?? "", "unique-identifier");
  let uid = "";
  for (const m of epub.opf.matchAll(/<dc:identifier\b([^>]*)>([\s\S]*?)<\/dc:identifier>/gi)) {
    if (attr(m[1], "id") === uidKey) uid = unescape(m[2]);
  }
  if (!uid) return data;
  /** @type {Uint8Array} */
  let key;
  if (algorithm === ADOBE_OBFUSCATION) {
    const hex = uid.slice(uid.lastIndexOf(":") + 1).replace(/[{}-]/g, "");
    if (!/^[0-9a-f]{32}$/i.test(hex)) return data;
    key = new Uint8Array(16);
    for (let i = 0; i < 16; i++) key[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  } else {
    key = sha1(new TextEncoder().encode(uid.replace(/[ \u0009\u000d\u000a]/g, "")));
  }
  const len = Math.min(algorithm === ADOBE_OBFUSCATION ? 1024 : 1040, data.length);
  const out = new Uint8Array(data);
  for (let i = 0; i < len; i++) out[i] = data[i] ^ key[i % key.length];
  return out;
}

/** SHA-1, for the IDPF font key (the runtime has no crypto). @param {Uint8Array} msg */
export function sha1(msg) {
  const ml = msg.length;
  const withPad = new Uint8Array(((ml + 9 + 63) >> 6) << 6);
  withPad.set(msg);
  withPad[ml] = 0x80;
  const dv = new DataView(withPad.buffer);
  dv.setUint32(withPad.length - 4, (ml * 8) >>> 0);
  dv.setUint32(withPad.length - 8, Math.floor((ml * 8) / 0x100000000));
  let [h0, h1, h2, h3, h4] = [0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0];
  const w = new Uint32Array(80);
  for (let off = 0; off < withPad.length; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = dv.getUint32(off + i * 4);
    for (let i = 16; i < 80; i++) {
      const x = w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16];
      w[i] = (x << 1) | (x >>> 31);
    }
    let [a, b, c, d, e] = [h0, h1, h2, h3, h4];
    for (let i = 0; i < 80; i++) {
      const f = i < 20 ? (b & c) | (~b & d) : i < 40 ? b ^ c ^ d : i < 60 ? (b & c) | (b & d) | (c & d) : b ^ c ^ d;
      const k = i < 20 ? 0x5a827999 : i < 40 ? 0x6ed9eba1 : i < 60 ? 0x8f1bbcdc : 0xca62c1d6;
      const t = (((a << 5) | (a >>> 27)) + f + e + k + w[i]) >>> 0;
      e = d;
      d = c;
      c = (b << 30) | (b >>> 2);
      b = a;
      a = t;
    }
    h0 = (h0 + a) >>> 0;
    h1 = (h1 + b) >>> 0;
    h2 = (h2 + c) >>> 0;
    h3 = (h3 + d) >>> 0;
    h4 = (h4 + e) >>> 0;
  }
  const out = new Uint8Array(20);
  const ov = new DataView(out.buffer);
  [h0, h1, h2, h3, h4].forEach((h, i) => ov.setUint32(i * 4, h));
  return out;
}

/**
 * The book's cover image: the manifest item with the cover-image property,
 * else the one <meta name="cover"> names.
 * @param {import("./epub.js").Epub} epub
 */
export function coverImage(epub) {
  const byProp = epub.manifest.find((i) => /\bcover-image\b/.test(i.props) && epub.files[i.path]);
  if (byProp) return byProp;
  const meta = /<meta\b[^>]*\bname\s*=\s*["']cover["'][^>]*>/i.exec(epub.opf)?.[0];
  const id = meta ? attr(meta, "content") : "";
  return epub.manifest.find((i) => i.id === id && i.type.startsWith("image/") && epub.files[i.path]);
}

/** A file name's extension, lower case, without the dot. @param {string} path */
export function extOf(path) {
  const b = baseName(path);
  return b.includes(".") ? b.slice(b.lastIndexOf(".") + 1).toLowerCase() : "";
}

/** posixpath.normpath for zip paths (no leading slash). @param {string} path */
export function normpath(path) {
  /** @type {string[]} */
  const out = [];
  for (const p of path.split("/")) {
    if (p === "..") out.pop();
    else if (p !== "." && p !== "") out.push(p);
  }
  return out.join("/");
}

/** decodeURIComponent that leaves malformed escapes alone. @param {string} s */
export function unquote(s) {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}
