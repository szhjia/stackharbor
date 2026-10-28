import { readFileSync, mkdtempSync, mkdirSync, writeFileSync, copyFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, it } from "vitest";

const root = resolve(import.meta.dirname, "../..");
it("keeps every locked Vite and Undici copy past the patched security floors", () => {
  // GHSA-587h-7wmj-h5pm and related Vite fixes; GHSA-xjj6-f33q-929c.
  const lock = JSON.parse(readFileSync(resolve(root, "package-lock.json"), "utf8"));
  for (const [name, minimum] of [["vite", [8, 3, 5]], ["undici", [8, 12, 2]]] as const) {
    const copies = Object.entries(lock.packages).filter(([path]) => path.endsWith(`/node_modules/${name}`) || path === `node_modules/${name}`);
    expect(copies.length, `Missing ${name} in the reproducible install`).toBeGreaterThan(0);
    for (const [path, meta] of copies) {
      const version = (meta as {version: string}).version;
      expect(version, path).toMatch(/^\d+\.\d+\.\d+$/);
      const parts = version.split(".").map(Number);
      const firstDifference = minimum.findIndex((value, i) => parts[i] !== value);
      expect(firstDifference < 0 || parts[firstDifference] > minimum[firstDifference], `${path}@${version} is below ${minimum.join(".")}`).toBe(true);
    }
  }
});

it("keeps retained frontend notices consistent with the locked runtime dependencies", () => {
  const result = spawnSync(process.execPath, [resolve(root, "../scripts/frontend-licenses.mjs"), "--check"], {encoding: "utf8"});
  expect(result.status, result.stderr).toBe(0);
});

it("rejects stale notices after a runtime dependency is removed and accepts regeneration", () => {
  const fixture = mkdtempSync(resolve(tmpdir(), "stackharbor-licenses-"));
  try {
    const write = (file: string, text: string) => {
      const path = resolve(fixture, file);
      mkdirSync(resolve(path, ".."), {recursive: true});
      writeFileSync(path, text);
    };
    const packages: Record<string, {dev?: boolean}> = {
      "": {}, "node_modules/kept-runtime": {}, "node_modules/removed-runtime": {}, "node_modules/build-only": {dev: true},
    };
    const lock = () => write("web/package-lock.json", JSON.stringify({packages}));
    lock();
    write("licenses/npm__shadcn-4.21.1-LICENSE.md", "Retained copied component license");
    for (const name of ["kept-runtime", "removed-runtime"]) {
      write(`web/node_modules/${name}/package.json`, JSON.stringify({name, version: "1.0.0", license: "MIT"}));
      write(`web/node_modules/${name}/LICENSE`, `License for ${name}`);
    }
    mkdirSync(resolve(fixture, "scripts"));
    const script = resolve(fixture, "scripts/frontend-licenses.mjs");
    copyFileSync(resolve(root, "../scripts/frontend-licenses.mjs"), script);
    const run = (...args: string[]) => spawnSync(process.execPath, [script, ...args], {encoding: "utf8"});
    expect(run().status).toBe(0);
    expect(run("--check").status).toBe(0);
    delete packages["node_modules/removed-runtime"];
    lock();
    const stale = run("--check");
    expect(stale.status).not.toBe(0);
    expect(stale.stderr).toContain("Frontend notices are stale");
    expect(run().status).toBe(0);
    expect(run("--check").status).toBe(0);
    const notices = readFileSync(resolve(fixture, "licenses/frontend-NOTICES"), "utf8");
    expect(notices).toContain("kept-runtime 1.0.0");
    expect(notices).not.toContain("removed-runtime");
    expect(notices).not.toContain("\n- build-only ");
    expect(notices).toContain("shadcn 4.21.1 copied components and CSS");
  } finally {
    rmSync(fixture, {recursive: true, force: true});
  }
});

it("keeps the unpatched braces CLI dependency chain out of reproducible installs", () => {
  const lock = JSON.parse(readFileSync(resolve(root, "package-lock.json"), "utf8"));
  const vulnerable = Object.keys(lock.packages).filter(path => /(?:^|\/)node_modules\/(?:braces|shadcn)$/.test(path));
  expect(vulnerable).toEqual([]);
  expect(readFileSync(resolve(root, "src/index.css"), "utf8")).not.toContain('"shadcn/tailwind.css"');
});

it("rejects an embedded bundle after its vendored stylesheet changes", () => {
  const fixture = mkdtempSync(resolve(tmpdir(), "stackharbor-assets-security-"));
  try {
    const inputs: Record<string, string> = {
      "internal/buildinfo/version.go": 'var Version = "0.6.1"',
      "internal/sessionapi/transport.go": "const ProtocolVersion = 1",
      "internal/web/dist/index.html": "<html></html>",
      "internal/web/dist/assets/app.js": "// fixture",
      "web/src/index.css": '@import "../vendor/shadcn/tailwind.css";',
      "web/vendor/shadcn/tailwind.css": "/* original */",
      "web/package.json": "{}", "web/package-lock.json": "{}",
      "web/index.html": "", "web/vite.config.ts": "", "web/tsconfig.json": "{}",
    };
    for (const [file, content] of Object.entries(inputs)) {
      const path = resolve(fixture, file);
      mkdirSync(resolve(path, ".."), {recursive: true});
      writeFileSync(path, content);
    }
    mkdirSync(resolve(fixture, "scripts"));
    const script = resolve(fixture, "scripts/web-assets.mjs");
    copyFileSync(resolve(root, "../scripts/web-assets.mjs"), script);
    const run = (action: string) => spawnSync(process.execPath, [script, action], {encoding: "utf8"});
    expect(run("generate").status).toBe(0);
    expect(run("validate").status).toBe(0);
    writeFileSync(resolve(fixture, "web/vendor/shadcn/tailwind.css"), "/* changed */");
    const changed = run("validate");
    expect(changed.status).not.toBe(0);
    expect(changed.stderr).toContain("Frontend assets mismatch");
  } finally {
    rmSync(fixture, {recursive: true, force: true});
  }
});
