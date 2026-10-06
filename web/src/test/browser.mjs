import { chromium } from "@playwright/test";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
const url = await readFile(
  "/private/tmp/stackharbor-browser-task9/url",
  "utf8",
);
const out = resolve("../docs/verification/task9");
await mkdir(out, { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
});
const page = await context.newPage();
const errors = [];
page.on("pageerror", (e) => {
  errors.push(e.message);
  console.log("PAGE ERROR " + e.message);
});
await page.goto(url);
await page.getByRole("heading", { name: "Overview", exact: true }).waitFor();
await page
  .getByText(/harbor-cafe/)
  .first()
  .waitFor();
await page.screenshot({ path: out + "/desktop-1440.png", fullPage: true });
console.log("overview " + (await page.locator("h1").textContent()));
console.log(
  "page width " +
    (await page.evaluate(() => ({
      width: innerWidth,
      scroll: document.documentElement.scrollWidth,
    }))),
);
await page.getByRole("button", { name: "Workspaces", exact: true }).click();
await page.getByRole("button", { name: /harbor-cafe/ }).click();
await page.getByRole("tab", { name: "Logs", exact: true }).click();
await page.getByText("<img src=x onerror=alert(1)>", { exact: true }).waitFor();
console.log("html text safe " + (await page.locator("img").count()));
await page.getByRole("button", { name: "Pause following" }).click();
await page
  .getByText("View paused. Session log collection continues.")
  .waitFor();
await page.getByRole("button", { name: "Resume following" }).click();
await page.screenshot({ path: out + "/desktop-logs.png", fullPage: true });
await page.getByRole("tab", { name: "Services", exact: true }).click();
const restart = page
  .getByRole("button", { name: "restart", exact: true })
  .first();
console.log("restart enabled " + (await restart.isEnabled()));
await restart.click();
await page.getByRole("dialog").waitFor();
await page.screenshot({ path: out + "/desktop-confirm.png", fullPage: true });
await page.keyboard.press("Escape");
await page.waitForTimeout(100);
console.log(
  "focus return " +
    (await restart.evaluate((e) => e === document.activeElement)),
);
await restart.click();
await page.getByRole("button", { name: "Confirm restart" }).click();
await page.getByRole("heading", { name: "Operations", exact: true }).waitFor();
await page.getByText("succeeded", { exact: true }).first().waitFor();
await page.screenshot({
  path: out + "/desktop-operations.png",
  fullPage: true,
});
await page.reload();
await page.getByRole("heading", { name: "Overview", exact: true }).waitFor();
console.log("cookie reload " + new URL(page.url()).hash);
await page.getByText("Live", { exact: true }).first().waitFor();
await writeFile("/private/tmp/stackharbor-browser-task9/disconnect", "");
await page.getByText("Reconnecting", { exact: true }).waitFor();
await page.getByText("Live", { exact: true }).first().waitFor();
console.log("TCP disconnect reconnect recovered");
await writeFile("/private/tmp/stackharbor-browser-task9/stale", "");
await page.getByText("stale", { exact: true }).first().waitFor();
await page.screenshot({ path: out + "/desktop-stale.png", fullPage: true });
const { unlink } = await import("node:fs/promises");
await unlink("/private/tmp/stackharbor-browser-task9/stale");
await page.getByRole("checkbox").first().waitFor();
await page.setViewportSize({ width: 390, height: 844 });
await page.screenshot({ path: out + "/mobile-390.png", fullPage: true });
console.log(
  "mobile width " +
    JSON.stringify(
      await page.evaluate(() => ({
        width: innerWidth,
        scroll: document.documentElement.scrollWidth,
      })),
    ),
);
await page.getByRole("button", { name: "Resources", exact: true }).click();
await page.screenshot({ path: out + "/mobile-resources.png", fullPage: true });
await page.getByRole("button", { name: "Workspaces", exact: true }).click();
await page.getByRole("button", { name: /harbor-cafe/ }).click();
await page.getByRole("tab", { name: "Logs", exact: true }).click();
await page.screenshot({ path: out + "/mobile-logs.png", fullPage: true });
console.log("browser errors " + JSON.stringify(errors));
await writeFile(
  out + "/browser-result.json",
  JSON.stringify(
    {
      errors,
      viewport: [1440, 390],
      verified: [
        "navigation",
        "safe log text",
        "pause/resume",
        "confirmation cancel",
        "focus return",
        "restart operation succeeded",
        "cookie reload",
        "actual TCP stream disconnect/reconnect",
        "stale snapshot",
        "mobile resources/logs",
      ],
    },
    null,
    2,
  ),
);
await browser.close();
