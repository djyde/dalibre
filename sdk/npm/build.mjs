// Makes the npm packages of dalibre-plugin from the release's binaries:
// one package per platform, holding its binary, and dalibre-plugin,
// which depends on them all optionally and runs the one npm installed
// (as esbuild does).
//
//   node sdk/npm/build.mjs <version> <bin-dir> <out-dir>
//
// <bin-dir> holds dalibre-plugin-<os>-<arch>[.exe], as the release builds
// them. Publish each folder in <out-dir>, the platforms' first.

import { mkdir, writeFile, copyFile, chmod, readFile } from "node:fs/promises";
import { join } from "node:path";

const [version, binDir, outDir] = process.argv.slice(2);
if (!version || !binDir || !outDir) {
  console.error("usage: node sdk/npm/build.mjs <version> <bin-dir> <out-dir>");
  process.exit(2);
}

// Go's names, and npm's (process.platform, process.arch).
const platforms = [
  ["darwin", "arm64", "darwin", "arm64"],
  ["darwin", "amd64", "darwin", "x64"],
  ["linux", "arm64", "linux", "arm64"],
  ["linux", "amd64", "linux", "x64"],
  ["windows", "arm64", "win32", "arm64"],
  ["windows", "amd64", "win32", "x64"],
];
const repository = { type: "git", url: "git+https://github.com/djyde/dalibre.git", directory: "sdk/npm" };
const license = "MIT";

const optional = {};
for (const [goos, goarch, os, cpu] of platforms) {
  const name = `dalibre-plugin-${os}-${cpu}`;
  const exe = goos === "windows" ? ".exe" : "";
  const dir = join(outDir, name);
  await mkdir(join(dir, "bin"), { recursive: true });
  await copyFile(join(binDir, `dalibre-plugin-${goos}-${goarch}${exe}`), join(dir, "bin", `dalibre-plugin${exe}`));
  await chmod(join(dir, "bin", `dalibre-plugin${exe}`), 0o755);
  await writeFile(join(dir, "package.json"), JSON.stringify({
    name, version, description: `dalibre-plugin for ${os} ${cpu}`, repository, license,
    os: [os], cpu: [cpu], files: ["bin"], preferUnplugged: true,
  }, null, 2) + "\n");
  optional[name] = version;
}

const main = join(outDir, "dalibre-plugin");
await mkdir(join(main, "bin"), { recursive: true });
await writeFile(join(main, "bin", "dalibre-plugin.js"), await readFile(new URL("./dalibre-plugin.js", import.meta.url)));
await chmod(join(main, "bin", "dalibre-plugin.js"), 0o755);
await copyFile(new URL("./README.md", import.meta.url), join(main, "README.md"));
await writeFile(join(main, "package.json"), JSON.stringify({
  name: "dalibre-plugin", version,
  description: "Write, try and pack Dalibre plugins",
  homepage: "https://github.com/djyde/dalibre/blob/main/docs/plugins/README.md",
  repository, license, bin: { "dalibre-plugin": "bin/dalibre-plugin.js" },
  files: ["bin"], optionalDependencies: optional,
}, null, 2) + "\n");
console.log(`packages for dalibre-plugin ${version} in ${outDir}`);
