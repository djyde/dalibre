// @ts-check
// Reading and writing EPUBs without a DOM: the zip through fflate, the
// OPF and XHTML through small, forgiving regular expressions.

import { unzipSync, zipSync, strFromU8, strToU8 } from "fflate";
import { readFile, writeFile } from "node:fs/promises";

export { strFromU8, strToU8 };

/**
 * An EPUB opened in memory.
 * @typedef {{
 *   files: Record<string, Uint8Array>,
 *   opfPath: string,
 *   opfDir: string,
 *   opf: string,
 *   manifest: { id: string, href: string, path: string, type: string, props: string }[],
 *   spine: { idref: string, linear: boolean }[],
 * }} Epub
 */

/** @param {string} path @returns {Promise<Epub>} */
export async function openEpub(path) {
  const data = await readFile(path);
  return parseEpub(new Uint8Array(data));
}

/** @param {Uint8Array} data @returns {Epub} */
export function parseEpub(data) {
  let files;
  try {
    files = unzipSync(data);
  } catch {
    throw dalibre.error("invalid", "This file isn't a valid EPUB: it can't be unzipped.");
  }
  const container = files["META-INF/container.xml"];
  const m = container && /full-path\s*=\s*["']([^"']+)["']/.exec(strFromU8(container));
  const opfPath = m ? decodeURIComponent(m[1]) : Object.keys(files).find((f) => f.endsWith(".opf"));
  if (!opfPath || !files[opfPath]) throw dalibre.error("invalid", "This EPUB has no package file (OPF).");
  const opfDir = opfPath.includes("/") ? opfPath.slice(0, opfPath.lastIndexOf("/") + 1) : "";
  const epub = { files, opfPath, opfDir, opf: strFromU8(files[opfPath]), manifest: [], spine: [] };
  reparse(epub);
  return epub;
}

/** Reads the manifest and spine again, after the OPF changed. @param {Epub} epub */
export function reparse(epub) {
  epub.manifest = [];
  for (const tag of epub.opf.match(/<(?:\w+:)?item\b[^>]*>/g) ?? []) {
    const href = attr(tag, "href");
    if (!href) continue;
    epub.manifest.push({
      id: attr(tag, "id"),
      href,
      path: resolve(epub.opfDir, href),
      type: attr(tag, "media-type"),
      props: attr(tag, "properties"),
    });
  }
  epub.spine = [];
  for (const tag of epub.opf.match(/<(?:\w+:)?itemref\b[^>]*>/g) ?? []) {
    epub.spine.push({ idref: attr(tag, "idref"), linear: attr(tag, "linear") !== "no" });
  }
}

/** The spine's documents, in reading order. @param {Epub} epub */
export function spineDocs(epub) {
  const byId = new Map(epub.manifest.map((i) => [i.id, i]));
  return epub.spine.map((s) => ({ ...s, item: byId.get(s.idref) })).filter((s) => s.item && epub.files[s.item.path]);
}

/** Text of a file in the EPUB. @param {Epub} epub @param {string} path */
export function text(epub, path) {
  const f = epub.files[path];
  return f ? strFromU8(f) : "";
}

/** @param {Epub} epub @param {string} path @param {string} s */
export function setText(epub, path, s) {
  epub.files[path] = strToU8(s);
  if (path === epub.opfPath) {
    epub.opf = s;
    reparse(epub);
  }
}

/** Writes the OPF back after editing epub.opf. @param {Epub} epub */
export function saveOpf(epub) {
  setText(epub, epub.opfPath, epub.opf);
}

/** The EPUB as a zip, mimetype first and stored, as the spec asks. @param {Epub} epub */
export function build(epub) {
  /** @type {import("fflate").Zippable} */
  const out = { mimetype: [strToU8("application/epub+zip"), { level: 0 }] };
  for (const [name, data] of Object.entries(epub.files)) {
    if (name === "mimetype" || name.endsWith("/")) continue;
    out[name] = [data, { level: /\.(jpe?g|png|gif|webp|woff2?|mp3|mp4)$/i.test(name) ? 0 : 6 }];
  }
  return zipSync(out);
}

/** @param {Epub} epub @param {string} path */
export async function save(epub, path) {
  await writeFile(path, build(epub));
}

/** An attribute's value in a tag, unescaped. @param {string} tag @param {string} name */
export function attr(tag, name) {
  const m = new RegExp(`\\s${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)')`, "i").exec(tag);
  return m ? unescape(m[1] ?? m[2]) : "";
}

/** A path relative to dir, with ./ and ../ resolved and fragments dropped. @param {string} dir @param {string} href */
export function resolve(dir, href) {
  const clean = decodeURIComponent(href.split("#")[0]);
  const parts = (clean.startsWith("/") ? clean.slice(1) : dir + clean).split("/");
  /** @type {string[]} */
  const out = [];
  for (const p of parts) {
    if (p === "..") out.pop();
    else if (p !== "." && p !== "") out.push(p);
  }
  return out.join("/");
}

/** href from one file to another, both paths in the zip. @param {string} from @param {string} to */
export function relative(from, to) {
  const a = from.split("/").slice(0, -1);
  const b = to.split("/");
  let i = 0;
  while (i < a.length && i < b.length - 1 && a[i] === b[i]) i++;
  return [...a.slice(i).map(() => ".."), ...b.slice(i)].join("/");
}

/** @param {string} s */
export function unescape(s) {
  return s.replace(/&(#x[0-9a-f]+|#\d+|amp|lt|gt|quot|apos|nbsp);/gi, (_, e) => {
    const l = e.toLowerCase();
    if (l === "amp") return "&";
    if (l === "lt") return "<";
    if (l === "gt") return ">";
    if (l === "quot") return '"';
    if (l === "apos") return "'";
    if (l === "nbsp") return " ";
    const n = l.startsWith("#x") ? parseInt(l.slice(2), 16) : parseInt(l.slice(1), 10);
    return Number.isFinite(n) ? String.fromCodePoint(n) : "";
  });
}

/** @param {string} s */
export function escape(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

/** The readable text of an (X)HTML document: body only, tags out, entities decoded. @param {string} html */
export function plainText(html) {
  const body = /<body\b[^>]*>([\s\S]*)<\/body>/i.exec(html)?.[1] ?? html;
  return unescape(
    body
      .replace(/<(script|style)\b[\s\S]*?<\/\1>/gi, " ")
      .replace(/<\/(p|div|h[1-6]|li|tr|br|section|blockquote)>|<br\b[^>]*>/gi, "\n")
      .replace(/<[^>]+>/g, " "),
  ).replace(/[ \t ]+/g, " ");
}

/** The title in a document's <title> or first heading. @param {string} html */
export function docTitle(html) {
  const h = /<h[1-3]\b[^>]*>([\s\S]*?)<\/h[1-3]>/i.exec(html)?.[1] ?? /<title>([\s\S]*?)<\/title>/i.exec(html)?.[1] ?? "";
  return unescape(h.replace(/<[^>]+>/g, " ")).replace(/\s+/g, " ").trim();
}

/** The table of contents' entries (EPUB 3 nav, else NCX): label and target path. @param {Epub} epub */
export function toc(epub) {
  /** @type {{ label: string, path: string, frag: string }[]} */
  const out = [];
  const nav = epub.manifest.find((i) => /\bnav\b/.test(i.props));
  if (nav) {
    const html = text(epub, nav.path);
    const block = /<nav\b[^>]*epub:type\s*=\s*["'][^"']*\btoc\b[^"']*["'][^>]*>([\s\S]*?)<\/nav>/i.exec(html)?.[1] ?? html;
    const dir = nav.path.slice(0, nav.path.lastIndexOf("/") + 1);
    for (const m of block.matchAll(/<a\b([^>]*)>([\s\S]*?)<\/a>/gi)) {
      const href = attr(m[1], "href");
      if (!href) continue;
      out.push({ label: unescape(m[2].replace(/<[^>]+>/g, "")).replace(/\s+/g, " ").trim(), path: resolve(dir, href), frag: href.split("#")[1] ?? "" });
    }
    if (out.length) return out;
  }
  const ncx = epub.manifest.find((i) => i.type === "application/x-dtbncx+xml");
  if (ncx) {
    const xml = text(epub, ncx.path);
    const dir = ncx.path.slice(0, ncx.path.lastIndexOf("/") + 1);
    for (const m of xml.matchAll(/<navPoint\b[\s\S]*?<text>([\s\S]*?)<\/text>[\s\S]*?<content\b([^>]*)\/?>/gi)) {
      const src = attr(m[2], "src");
      if (src) out.push({ label: unescape(m[1]).replace(/\s+/g, " ").trim(), path: resolve(dir, src), frag: src.split("#")[1] ?? "" });
    }
  }
  return out;
}

/** A Dublin Core field's values from the OPF. @param {Epub} epub @param {string} name */
export function dc(epub, name) {
  const re = new RegExp(`<dc:${name}\\b[^>]*>([\\s\\S]*?)</dc:${name}>`, "gi");
  return [...epub.opf.matchAll(re)].map((m) => unescape(m[1]).trim()).filter(Boolean);
}

/** A file name safe for /work, with the format's extension. @param {string} title @param {string} ext */
export function fileName(title, ext) {
  const base = title.replace(/[\\/:*?"<>|\x00-\x1f]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 80) || "book";
  return `/work/${base}.${ext}`;
}

/** A random identifier for a new book. */
export function uuid() {
  const h = () => Math.floor(Math.random() * 0x10000).toString(16).padStart(4, "0");
  return `${h()}${h()}-${h()}-4${h().slice(1)}-${((Math.random() * 4) | 8).toString(16)}${h().slice(1)}-${h()}${h()}${h()}`;
}
