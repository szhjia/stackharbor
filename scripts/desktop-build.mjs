import { execFileSync } from "node:child_process";
import { cpSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL("../", import.meta.url));
const run = (command, args, cwd = root, env = process.env) => execFileSync(command, args, {cwd, env, stdio: "inherit"});
run("sh", ["scripts/web-build.sh"]);
run(process.execPath, ["scripts/desktop-prepare.mjs"]);
run("npm", ["ci"], join(root, "desktop"));
run("python3", ["scripts/desktop-licenses.py"]);
const target = readFileSync(join(root, "desktop/generated/target"), "utf8").trim();
const release = process.env.STACKHARBOR_DESKTOP_DISTRIBUTION === "release";
const env = {...process.env};
if (process.platform === "darwin") {
  if (release && (!env.APPLE_SIGNING_IDENTITY || env.APPLE_SIGNING_IDENTITY === "-" || !(env.APPLE_ID && env.APPLE_PASSWORD && env.APPLE_TEAM_ID || env.APPLE_API_KEY && env.APPLE_API_ISSUER && env.APPLE_API_KEY_PATH))) throw Error("Public macOS distribution requires signing identity and notarization credentials");
  if (!release) env.APPLE_SIGNING_IDENTITY = "-";
}
run("npm", ["run", "tauri", "--", "build", "--ci", "--target", target], join(root, "desktop"), env);
const metadata = JSON.parse(readFileSync(join(root, "desktop/generated/build.json"), "utf8"));
const distribution = release ? "release" : "internal";
const output = join(root, "dist/desktop", `${metadata.version}-${target}-${distribution}`);
mkdirSync(output, {recursive: true});
const bundle = join(root, "desktop/target", target, "release/bundle");
const installerPrefix = JSON.parse(readFileSync(join(root, "desktop/tauri.conf.json"), "utf8")).productName + `_${metadata.version}_`;
let count = 0;
for (const directory of readdirSync(bundle, {withFileTypes: true}).filter(e => e.isDirectory())) {
  for (const file of readdirSync(join(bundle, directory.name))) {
    if (!file.startsWith(installerPrefix) || !file.endsWith(".dmg") && !file.endsWith(".deb")) continue;
    cpSync(join(bundle, directory.name, file), join(output, file)); count++;
  }
}
if (!count) throw Error("Desktop bundling produced no installer");
writeFileSync(join(output, "build.json"), JSON.stringify({...metadata, distribution}, null, 2) + "\n");
writeFileSync(join(output, "README.txt"), release ? "Release installers; validate platform signature before installation.\n" : "INTERNAL TEST BUILD. Not a signed/notarized public release. Installing starts no process.\n");
writeFileSync(join(output, "SHA256SUMS"), readdirSync(output).filter(name => name !== "SHA256SUMS").sort().map(name => `${createHash("sha256").update(readFileSync(join(output, name))).digest("hex")}  ${name}`).join("\n") + "\n");
console.log(`Desktop installers: ${output}`);
