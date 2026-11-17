// Bundles each plugin's source (src/<name>/main.js, with src/shared and
// npm packages) into plugins/<name>/main.js. marketplace.json is written
// by index.mjs.
//
//   node build.mjs            build every plugin
//   node build.mjs epub-merge build one

import { build } from "esbuild";
import { readdir, stat } from "node:fs/promises";
import { existsSync } from "node:fs";


const only = process.argv.slice(2);
const names = (await readdir("plugins")).filter((n) => !n.startsWith(".") && (only.length === 0 || only.includes(n)));

for (const name of names) {
  const entry = `src/${name}/main.js`;
  if (!existsSync(entry)) continue;
  await build({
    entryPoints: [entry],
    bundle: true,
    format: "esm",
    target: "es2023",
    external: ["node:*"],
    outfile: `plugins/${name}/main.js`,
    legalComments: "none",
    banner: { js: `// Built from marketplace/src/${name} in https://github.com/djyde/dalibre. Edit the source, not this file.` },
    logLevel: "warning",
  });
  const { size } = await stat(`plugins/${name}/main.js`);
  console.log(`${name}: ${(size / 1024).toFixed(0)} KB`);
}
