import { readFileSync, writeFileSync, readdirSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
const root=fileURLToPath(new URL("../",import.meta.url));
const lock=JSON.parse(readFileSync(resolve(root,"web/package-lock.json"),"utf8"));
const check=process.argv.includes("--check");
const entries=[];
for(const [path,meta] of Object.entries(lock.packages)) {
 if(!path || meta.dev && !["node_modules/shadcn","node_modules/tailwindcss"].includes(path)) continue;
 const dir=resolve(root,"web",path);
 const pkg=JSON.parse(readFileSync(resolve(dir,"package.json"),"utf8"));
 const names=readdirSync(dir).filter(n=>/^(LICENSE|LICENCE|COPYING|NOTICE)([.-]|$)/i.test(n));
 if(!names.length) {
  // npm omits this upstream license; retain its separately sourced text.
  if(pkg.name!=="react-remove-scroll-bar"||pkg.version!=="2.3.8") throw Error(`No license text for ${pkg.name}@${pkg.version}`);
  const target=`npm__react-remove-scroll-bar-2.3.8-LICENSE`;
  if(!existsSync(resolve(root,"licenses",target))) throw Error(`Missing upstream ${target}`);
  entries.push(`- ${pkg.name} ${pkg.version} (MIT): licenses/${target} (upstream LICENSE at8ca9ba5ea52de03308fe8ced94f7b159a44d28ff; npm tarball omits it)`);
  continue;
 }
 const target=`npm__${pkg.name.replaceAll('/','__').replaceAll('@','')}-${pkg.version}`;
 for(const name of names) {
  const text=readFileSync(resolve(dir,name));
  const destination=resolve(root,"licenses",target+"-"+name);
  if(check) {if(!existsSync(destination)||!readFileSync(destination).equals(text)) throw Error(`Missing/stale ${destination}`);}
  else writeFileSync(destination,text);
 }
 entries.push(`- ${pkg.name} ${pkg.version} (${pkg.license ?? 'see license text'}): licenses/${target}-${names[0]}`);
}
entries.sort();
const text=`Frontend third-party notices\n\nThese pinned npm packages contribute runtime JavaScript, copied shadcn/ui\ncomponents, or imported CSS to the embedded browser console. Source/build-only\ntransitive CLI dependencies are not shipped in the executable. See web/package-lock.json\nfor the complete reproducible source toolchain. shadcn/ui component sources and\nshadcn/tailwind.css are MIT, covered by the shadcn license below.\n\n${entries.join('\n')}\n`;
const destination=resolve(root,"licenses/frontend-NOTICES");
if(check) {if(readFileSync(destination,"utf8")!==text) throw Error("Frontend notices are stale; run node scripts/frontend-licenses.mjs");}
else writeFileSync(destination,text);
console.log(`Verified frontend notices/licenses: ${entries.length} packages`);
