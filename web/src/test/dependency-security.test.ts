import { readFileSync, mkdtempSync, mkdirSync, writeFileSync, copyFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, it } from "vitest";

const root = resolve(import.meta.dirname, "../..");
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
