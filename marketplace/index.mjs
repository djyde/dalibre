// Writes marketplace.json, the index Dalibre's Marketplace reads, from
// the plugins here (plugins/*, packed into the marketplace branch's
// packages/ as <id>-<version>.dalibre-plugin) and the community's
// (community.json: repositories, each at its latest release).
//
//   node index.mjs [--out FILE]   write marketplace.json (here by default)
//   node index.mjs --check        only check community.json's plugins
//
// GITHUB_TOKEN raises GitHub's rate limit. DALIBRE_PLUGIN, the path of a
// dalibre-plugin binary, runs `dalibre-plugin check` on each community
// plugin too.

import { readdir, readFile, writeFile, mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { execFileSync } from "node:child_process";
import { unzipSync, strFromU8 } from "fflate";

const REPO = "djyde/dalibre";
// Where the marketplace branch is served from.
const PACKAGES = "https://raw.githubusercontent.com/djyde/dalibre/refs/heads/marketplace/packages";
const outIndex = process.argv.indexOf("--out");
const out = outIndex > 0 ? process.argv[outIndex + 1] : "marketplace.json";
const checkOnly = process.argv.includes("--check");
const token = process.env.GITHUB_TOKEN;
const reserved = (id) => id === "dalibre" || id.startsWith("dalibre.");
const semver = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$/;

const catalog = JSON.parse(await readFile("catalog.json", "utf8"));
const community = JSON.parse(await readFile("community.json", "utf8"));
const categories = new Set(catalog.categories.map((c) => c.id));
const downloads = await calibreDownloads();

/** The listing of a plugin, from its manifest. */
function listing(m, extra) {
  return {
    id: m.id,
    name: m.name,
    version: m.version,
    description: m.description,
    author: m.author,
    license: m.license,
    icon: m.icon,
    homepage: m.homepage,
    dalibre: m.dalibre,
    category: extra.category,
    calibre: extra.calibre,
    downloads: extra.calibre ? downloads.get(extra.calibre.toLowerCase()) : undefined,
    repo: extra.repo,
    community: extra.community || undefined,
    package: extra.package,
    capabilities: m.capabilities,
    permissions: m.permissions ?? { hosts: [] },
  };
}

const plugins = [];
const errors = [];

if (!checkOnly) {
  for (const name of (await readdir("plugins")).filter((n) => !n.startsWith(".")).sort()) {
    const m = JSON.parse(await readFile(`plugins/${name}/plugin.json`, "utf8"));
    const extra = catalog.plugins[name] ?? {};
    if (!categories.has(extra.category)) errors.push(`plugins/${name}: catalog.json gives it no known category`);
    plugins.push(listing(m, {
      ...extra,
      repo: REPO,
      package: `${PACKAGES}/${m.id}-${m.version}.dalibre-plugin`,
    }));
  }
}

const seen = new Set(plugins.map((p) => p.id));
for (const entry of community.plugins) {
  try {
    const l = await resolveCommunity(entry);
    if (seen.has(l.id)) throw new Error(`id ${l.id} is already listed`);
    seen.add(l.id);
    plugins.push(l);
    console.log(`${entry.repo}: ${l.name} ${l.version}`);
  } catch (err) {
    errors.push(`${entry.repo}: ${err.message}`);
  }
}

for (const e of errors) console.error(`✗ ${e}`);
if (checkOnly) process.exit(errors.length ? 1 : 0);
if (errors.some((e) => e.startsWith("plugins/"))) process.exit(1);

await writeFile(
  out,
  JSON.stringify({ name: catalog.name, categories: catalog.categories, plugins }, null, 2) + "\n",
);
console.log(`marketplace.json: ${plugins.length} plugins`);

/** A community plugin's listing: its latest release, checked. */
async function resolveCommunity(entry) {
  if (!/^[\w.-]+\/[\w.-]+$/.test(entry.repo ?? "")) throw new Error("repo must be owner/name");
  if (!categories.has(entry.category)) throw new Error(`category must be one of ${[...categories].join(", ")}`);
  const rel = await gh(`/repos/${entry.repo}/releases/latest`);
  if (!rel) throw new Error("it has no release: publish one, as Dalibre installs releases");
  const asset = rel.assets.find((a) => a.name.endsWith(".dalibre-plugin"));
  const pkg = asset ? asset.browser_download_url : `https://api.github.com/repos/${entry.repo}/zipball/${encodeURIComponent(rel.tag_name)}`;
  const res = await fetch(pkg, { headers: auth() });
  if (!res.ok) throw new Error(`its package didn’t download: ${res.status}`);
  const files = unzipSync(new Uint8Array(await res.arrayBuffer()));
  const root = packageRoot(Object.keys(files));
  const m = JSON.parse(strFromU8(files[root + "plugin.json"]));
  for (const k of ["id", "name", "version", "capabilities"]) if (!m[k]) throw new Error(`plugin.json has no ${k}`);
  if (m.api !== 1) throw new Error("plugin.json must say api 1");
  if (reserved(m.id)) throw new Error(`id ${m.id} is kept for Dalibre’s own plugins`);
  if (!semver.test(m.version)) throw new Error(`version ${m.version} isn’t a semantic version`);
  if (process.env.DALIBRE_PLUGIN) {
    const dir = await mkdtemp(join(tmpdir(), "market-"));
    try {
      for (const [name, data] of Object.entries(files)) {
        if (!name.startsWith(root) || name.endsWith("/")) continue;
        const dst = join(dir, name.slice(root.length));
        await mkdir(dirname(dst), { recursive: true });
        await writeFile(dst, data);
      }
      execFileSync(process.env.DALIBRE_PLUGIN, ["check", dir], { stdio: "pipe" });
    } catch (err) {
      throw new Error(`dalibre-plugin check failed: ${err.stderr?.toString().trim() || err.message}`);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  }
  return listing(m, { category: entry.category, calibre: entry.calibre, repo: entry.repo, community: true, package: pkg });
}

/** The folder holding plugin.json, as Dalibre finds it in a package. */
function packageRoot(names) {
  if (names.includes("plugin.json")) return "";
  const roots = names.filter((n) => /^[^/]+\/plugin\.json$/.test(n));
  if (roots.length === 1) return roots[0].slice(0, -"plugin.json".length);
  const nested = names.filter((n) => /^[^/]+\/plugin\/plugin\.json$/.test(n));
  if (nested.length === 1) return nested[0].slice(0, -"plugin.json".length);
  throw new Error("no plugin.json at the package’s root or in a plugin folder");
}

function auth() {
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function gh(path) {
  const res = await fetch(`https://api.github.com${path}`, { headers: { Accept: "application/vnd.github+json", ...auth() } });
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`GitHub answered ${res.status} for ${path}`);
  return res.json();
}

/** calibre plugins' total downloads, by lower-cased name, from calibre's stats page. */
async function calibreDownloads() {
  const out = new Map();
  try {
    const html = await (await fetch("https://plugins.calibre-ebook.com/stats.html")).text();
    for (const m of html.matchAll(/<tr[^>]*>\s*<td[^>]*>([\s\S]*?)<\/td>\s*<td[^>]*>\s*(\d+)\s*<\/td>/g)) {
      out.set(m[1].replace(/<[^>]+>/g, "").replace(/&amp;/g, "&").trim().toLowerCase(), Number(m[2]));
    }
  } catch (err) {
    console.warn(`calibre’s download counts unavailable: ${err.message}`);
  }
  return out;
}
