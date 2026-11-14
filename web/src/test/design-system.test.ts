import { readFileSync, readdirSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

const src = resolve(import.meta.dirname, "..");
function files(dir: string): string[] {
  return readdirSync(dir, {withFileTypes: true}).flatMap(entry =>
    entry.isDirectory() ? files(resolve(dir, entry.name)) : [resolve(dir, entry.name)],
  );
}
it("keeps visual values in one token source and application styles consume tokens", () => {
  const tokens = resolve(src, "styles/tokens.css");
  expect(existsSync(tokens), "Missing canonical design tokens").toBe(true);
  const violations: string[] = [];
  for (const path of files(src).filter(path => path.endsWith(".css") && path !== tokens)) {
    const css = readFileSync(path, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    for (const [index, line] of css.split("\n").entries()) {
      if (/#[\da-f]{3,8}\b|\b(?:oklch|rgba?|hsla?)\(/i.test(line) || /(?:^|[\s:])\d*\.?\d+(?:px|rem|ms)\b/.test(line)) {
        violations.push(`${path.slice(src.length + 1)}:${index + 1}: ${line.trim()}`);
      }
    }
  }
  expect(violations, "Move visual literals into styles/tokens.css").toEqual([]);
});
it("does not introduce raw palette or arbitrary visual values into JSX classes", () => {
  const violations: string[] = [];
  for (const path of files(src).filter(path => path.endsWith(".tsx") && !path.includes("/test/"))) {
    const source = readFileSync(path, "utf8");
    if (/(?:bg|text|border)-(?:white|black|gray|slate|red|blue|green)(?:\b|-)|\[(?:[^\]\n]*\d+(?:px|rem|ms)|#[\da-f]{3,8})\]/i.test(source)) violations.push(path.slice(src.length + 1));
  }
  expect(violations, "Use semantic token utilities in components").toEqual([]);
});
it("resolves every application CSS variable to a declared token or Radix runtime value", () => {
  const sources = files(src).filter(path => /\.(css|tsx)$/.test(path) && !path.includes("/test/"));
  const css = sources.filter(path => path.endsWith(".css")).map(path => readFileSync(path, "utf8")).join("\n");
  const declared = new Set([...css.matchAll(/(--[\w-]+)\s*:/g)].map(match => match[1]));
  const missing = new Set<string>();
  for (const path of sources) {
    for (const match of readFileSync(path, "utf8").matchAll(/var\((--[\w-]+)/g)) {
      if (!declared.has(match[1]) && !match[1].startsWith("--radix-")) missing.add(match[1]);
    }
  }
  expect([...missing]).toEqual([]);
});
it("separates sidebar and log surface semantics", () => {
  const tokens = readFileSync(resolve(src, "styles/tokens.css"), "utf8");
  const app = readFileSync(resolve(src, "styles/app.css"), "utf8");
  expect(tokens).toMatch(/--log-surface\s*:/);
  expect(tokens).toMatch(/--log-foreground\s*:/);
  const logBlock = app.match(/\.log-lines\s*\{([^}]*)\}/)?.[1] ?? "";
  expect(logBlock).toMatch(/background:\s*var\(--log-surface\)/);
  expect(logBlock).toMatch(/color:\s*var\(--log-foreground\)/);
  expect(logBlock).not.toMatch(/var\(--sidebar(?:-foreground)?\)/);
});
it("uses explicit token-backed transitions", () => {
  const sources = files(src).filter(path => /\.(css|tsx)$/.test(path) && !path.includes("/test/"));
  const violations: string[] = [];
  for (const path of sources) {
    const source = readFileSync(path, "utf8");
    if (/transition-all|transition:\s*all/.test(source)) violations.push(path.slice(src.length + 1));
  }
  expect(violations, "List transition properties explicitly").toEqual([]);
  const app = readFileSync(resolve(src, "styles/app.css"), "utf8");
  expect(app).not.toMatch(/var\(--motion-fade\)\s+ease\b/);
  for (const path of ["components/ui/button.tsx", "components/ui/navigation-menu.tsx", "components/ui/badge.tsx", "components/ui/tabs.tsx"]) {
    expect(readFileSync(resolve(src, path), "utf8")).toContain("transition-colors duration-(--motion-fast)");
  }
});
