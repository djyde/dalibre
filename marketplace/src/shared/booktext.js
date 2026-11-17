// @ts-check
// A book's file as the (X)HTML documents of its spine, for the formats
// that are text underneath: EPUB, KEPUB, HTMLZ, FB2 and TXT. calibre's
// plugins convert other formats to EPUB first and read its spine; this
// is the closest Dalibre gets without conversion.

import { unzipSync, deflateSync, strFromU8, strToU8 } from "fflate";
import { readFile } from "node:fs/promises";
import { parseEpub, spineDocs, text, escape, unescape } from "./epub.js";

/** Formats bookDocs reads. */
export const TEXT_FORMATS = ["EPUB", "KEPUB", "TXT", "HTMLZ", "FB2"];

/**
 * A document of a book: its markup, and its compressed size in the zip
 * (for EPUB and KEPUB, from the zip itself; else as deflated now).
 * @typedef {{ html: string, compressed: number }} BookDoc
 */

/**
 * The file's spine documents in reading order.
 * @param {{ path: string, format: string }} file
 * @returns {Promise<BookDoc[]>}
 */
export async function bookDocs(file) {
  const data = new Uint8Array(await readFile(file.path));
  const format = file.format.toUpperCase();
  if (format === "EPUB" || format === "KEPUB") {
    const epub = parseEpub(data);
    const sizes = zipSizes(data);
    return spineDocs(epub).map((d) => ({ html: text(epub, d.item.path), compressed: sizes.get(d.item.path) ?? deflated(text(epub, d.item.path)) }));
  }
  /** @param {string} html */
  const doc = (html) => ({ html, compressed: deflated(html) });
  if (format === "HTMLZ") {
    const files = unzipFiles(data);
    const name = Object.keys(files).find((n) => /(^|\/)index\.x?html?$/i.test(n)) ?? Object.keys(files).find((n) => /\.x?html?$/i.test(n));
    if (!name) throw dalibre.error("invalid", "This HTMLZ file has no HTML document.");
    return [doc(decode(files[name]))];
  }
  if (format === "FB2") {
    let xml;
    if (data[0] === 0x50 && data[1] === 0x4b) {
      const files = unzipFiles(data);
      const name = Object.keys(files).find((n) => /\.fb2$/i.test(n));
      if (!name) throw dalibre.error("invalid", "This FB2 archive has no .fb2 file.");
      xml = decode(files[name]);
    } else {
      xml = decode(data);
    }
    xml = xml.replace(/<binary\b[\s\S]*?<\/binary>/gi, "");
    const bodies = [...xml.matchAll(/<body\b[^>]*>([\s\S]*?)<\/body>/gi)].map((m) => m[1]);
    // One document per top-level section, as calibre's FB2 input splits it.
    const sections = bodies.flatMap((b) => b.split(/(?=<section\b)/i)).filter((s) => s.replace(/<[^>]+>/g, "").trim());
    return sections.map((s) => doc(`<html><body>${s.replace(/<(\/?)(?:v|text-author)\b/gi, "<$1p").replace(/<empty-line\s*\/>/gi, "<br/>")}</body></html>`));
  }
  if (format === "TXT") {
    // calibre's TXT input: a paragraph per block of lines; documents of about 20 KB.
    const paras = decode(data).replace(/\r\n?/g, "\n").split(/\n\s*\n|\n(?=[ \t　])/).map((p) => p.trim()).filter(Boolean);
    /** @type {BookDoc[]} */
    const out = [];
    let cur = "";
    for (const p of paras) {
      cur += `<p>${escape(p).replace(/\n/g, " ")}</p>\n`;
      if (cur.length > 20000) {
        out.push(doc(`<html><body>${cur}</body></html>`));
        cur = "";
      }
    }
    if (cur || out.length === 0) out.push(doc(`<html><body>${cur}</body></html>`));
    return out;
  }
  throw dalibre.error("invalid", `${format} files can't be read; use EPUB, KEPUB, TXT, HTMLZ or FB2.`);
}

/**
 * The text of a document's body, as calibre's plugins read it with
 * BeautifulSoup: every string stripped, joined by a space.
 * @param {string} html
 */
export function bodyText(html) {
  const body = /<body\b[^>]*>([\s\S]*)<\/body>/i.exec(html)?.[1] ?? html;
  const out = [];
  for (const s of body.replace(/<!--[\s\S]*?-->|<(script|style)\b[\s\S]*?<\/\1\s*>/gi, "").split(/<[^>]*>/)) {
    const t = unescape(s).trim();
    if (t) out.push(t);
  }
  return out.join(" ");
}

/** Compressed sizes of a zip's files, from its central directory. @param {Uint8Array} data */
function zipSizes(data) {
  /** @type {Map<string, number>} */
  const sizes = new Map();
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  let eocd = -1;
  for (let i = data.length - 22; i >= Math.max(0, data.length - 65557); i--) {
    if (view.getUint32(i, true) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) return sizes;
  let p = view.getUint32(eocd + 16, true);
  const n = view.getUint16(eocd + 10, true);
  for (let k = 0; k < n && p + 46 <= data.length; k++) {
    if (view.getUint32(p, true) !== 0x02014b50) break;
    const csize = view.getUint32(p + 20, true);
    const nameLen = view.getUint16(p + 28, true);
    const extra = view.getUint16(p + 30, true);
    const comment = view.getUint16(p + 32, true);
    sizes.set(strFromU8(data.subarray(p + 46, p + 46 + nameLen)), csize);
    p += 46 + nameLen + extra + comment;
  }
  return sizes;
}

/** @param {string} s */
function deflated(s) {
  return deflateSync(strToU8(s), { level: 6 }).length;
}

/** @param {Uint8Array} data */
function unzipFiles(data) {
  try {
    return unzipSync(data);
  } catch {
    throw dalibre.error("invalid", "This file can't be unzipped.");
  }
}

/**
 * Bytes as text: UTF-8 (with or without BOM), UTF-16 with a BOM, else
 * what the XML declaration says when the runtime knows it, else Latin-1.
 * @param {Uint8Array} data
 */
export function decode(data) {
  if (data[0] === 0xff && data[1] === 0xfe) return tryDecode("utf-16le", data.subarray(2)) ?? strFromU8(data);
  if (data[0] === 0xfe && data[1] === 0xff) return tryDecode("utf-16be", data.subarray(2)) ?? strFromU8(data);
  if (data[0] === 0xef && data[1] === 0xbb && data[2] === 0xbf) data = data.subarray(3);
  const utf8 = tryDecode("utf-8", data, true);
  if (utf8 !== null) return utf8;
  const head = String.fromCharCode(...data.subarray(0, 200));
  const declared = /encoding\s*=\s*["']([\w-]+)["']/i.exec(head)?.[1];
  for (const enc of [declared, "gb18030", "big5", "windows-1252"]) {
    if (!enc) continue;
    const s = tryDecode(enc, data);
    if (s !== null) return s;
  }
  let s = "";
  for (let i = 0; i < data.length; i += 8192) s += String.fromCharCode(...data.subarray(i, i + 8192));
  return s;
}

/** @param {string} enc @param {Uint8Array} data @param {boolean} [fatal] */
function tryDecode(enc, data, fatal = false) {
  try {
    return new TextDecoder(enc, { fatal }).decode(data);
  } catch {
    return null;
  }
}
