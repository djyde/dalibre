#!/usr/bin/env node
// Runs the dalibre-plugin binary npm installed for this platform.
const { spawnSync } = require("node:child_process");

const os = process.platform, cpu = process.arch;
const exe = os === "win32" ? ".exe" : "";
let bin;
try {
  bin = require.resolve(`dalibre-plugin-${os}-${cpu}/bin/dalibre-plugin${exe}`);
} catch {
  console.error(`dalibre-plugin has no build for ${os} ${cpu}; build it from https://github.com/djyde/dalibre: go build ./cmd/dalibre-plugin`);
  process.exit(1);
}
const r = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (r.error) {
  console.error(r.error.message);
  process.exit(1);
}
process.exit(r.status ?? 1);
