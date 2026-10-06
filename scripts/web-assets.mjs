import { readFileSync, writeFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve, relative } from "node:path";
import { createHash } from "node:crypto";
const root = fileURLToPath(new URL("../", import.meta.url));
const dist = resolve(root, "internal/web/dist");
const hash = (data) => createHash("sha256").update(data).digest("hex");
const version = readFileSync(resolve(root,"internal/buildinfo/version.go"),"utf8").match(/var Version = "([^"\n]+)"/)[1];
const protocol = Number(readFileSync(resolve(root,"internal/sessionapi/transport.go"),"utf8").match(/const ProtocolVersion = (\d+)/)[1]);
function files(dir) { return readdirSync(dir,{withFileTypes:true}).flatMap(e=> e.isDirectory()?files(resolve(dir,e.name)):[resolve(dir,e.name)]).sort(); }
function sourceDigest() {
 const paths = [...files(resolve(root,"web/src")),...['web/package.json','web/package-lock.json','web/index.html','web/vite.config.ts','web/tsconfig.json'].map(p=>resolve(root,p))].sort();
 return hash(paths.map(p=>relative(root,p)+"\0"+hash(readFileSync(p))).join("\n"));
}
const content = Object.fromEntries(files(dist).filter(p=>!p.endsWith('/asset-manifest.json')).map(p=>[relative(dist,p),hash(readFileSync(p))]));
const manifest = {version, protocol_version:protocol, source_sha256:sourceDigest(), files:content};
if (!content['index.html'] || !Object.keys(content).some(p=>p.startsWith('assets/')&&p.endsWith('.js'))) throw Error("Actual frontend index and JavaScript bundle required");
const path=resolve(dist,"asset-manifest.json");
if (process.argv[2]==="generate") writeFileSync(path,JSON.stringify(manifest,null,2)+"\n");
else if (process.argv[2]==="validate") {
 const actual=JSON.parse(readFileSync(path,"utf8"));
 if (JSON.stringify(actual)!==JSON.stringify(manifest)) throw Error("Frontend assets mismatch source/version/protocol; rebuild with make web-build");
 console.log(`Validated frontend ${version}, session protocol ${protocol}, ${Object.keys(content).length} files`);
} else throw Error("Usage: node scripts/web-assets.mjs generate|validate");
