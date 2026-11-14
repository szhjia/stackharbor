import { readdirSync, statSync } from "node:fs";
import { resolve } from "node:path";

const assetsDir = resolve(import.meta.dirname, "../internal/web/dist/assets");
const limit = 500 * 1024;
const chunks = readdirSync(assetsDir)
  .filter(name => name.endsWith(".js"))
  .map(name => ({ name, size: statSync(resolve(assetsDir, name)).size }));

if (chunks.length < 2) {
  throw new Error(`Expected route-level code splitting, found ${chunks.length} JavaScript chunk.`);
}
const oversized = chunks.filter(chunk => chunk.size > limit);
if (oversized.length) {
  throw new Error(`JavaScript chunks exceed 500 kB: ${oversized.map(chunk => `${chunk.name} ${(chunk.size / 1024).toFixed(2)} kB`).join(", ")}`);
}
const largest = Math.max(...chunks.map(chunk => chunk.size));
console.log(`Bundle budget passed: ${chunks.length} JavaScript chunks, largest ${(largest / 1024).toFixed(2)} kB.`);
