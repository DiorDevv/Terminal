import { execSync } from "node:child_process";
import * as os from "node:os";
import { AUTH_FILE, api, confirmDialog, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });

/** The address of this machine on the local network (squid refuses loopback destinations). */
function lanIP(): string {
  for (const list of Object.values(os.networkInterfaces())) {
    for (const i of list ?? []) if (i.family === "IPv4" && !i.internal) return i.address;
  }
  throw new Error("no network address");
}

/** One real request through the running squid. */
function viaSquid(url: string): string {
  try {
    return execSync(`curl -s -m 10 -o /dev/null -x http://127.0.0.1:3128 -w '%{http_code}' ${url}`).toString();
  } catch {
    return "curl-failed"; // squid may be busy applying a change for a moment
  }
}

const requestsTile = (page: import("@playwright/test").Page) =>
  page.locator("div", { hasText: /^So'rovlar$/ }).first().locator("xpath=following-sibling::div[1]");

test("the statistics page shows its tiles, chart, tables and live squid data", async ({ page }) => {
  await page.goto("/stats");
  for (const label of ["So'rovlar", "Trafik", "Keshdan", "Bloklangan", "Xatolar (5xx)", "Qurilmalar"]) {
    await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
  }
  await expect(page.getByText("Eng ko'p so'ralgan saytlar")).toBeVisible();
  await expect(page.getByText("Squid hozir")).toBeVisible();
  await expect(page.getByText("ishlayapti", { exact: true })).toBeVisible();
  await expect(page.getByText("Versiya")).toBeVisible();
  await expect(page.getByText("Disk", { exact: true })).toBeVisible();

  // the ranges switch and the chart can be read as a table
  for (const range of ["7 kun", "30 kun", "24 soat"]) {
    await page.getByRole("button", { name: range, exact: true }).click();
    await expect(page.getByRole("button", { name: range, exact: true })).toHaveClass(/indigo/);
  }
  await page.getByRole("button", { name: "Jadval ko'rinishi" }).click();
  await expect(page.locator("table").first()).toContainText("Vaqt");
  await page.getByRole("button", { name: "Grafikni ko'rsatish" }).click();
  await expect(page.locator("svg[role=img]")).toBeVisible();
});

test("real traffic through squid shows up in the statistics", async ({ page }) => {
  await page.goto("/stats");
  const before = Number((await api(page, "GET", "/monitor/summary?range=24h")).json.requests);
  const target = `http://${lanIP()}:8080/api/health`;
  for (let i = 0; i < 5; i++) expect(viaSquid(target)).toBe("200");

  // the log reader polls every few seconds
  await expect
    .poll(async () => Number((await api(page, "GET", "/monitor/summary?range=24h")).json.requests), { timeout: 30_000, intervals: [2000] })
    .toBeGreaterThanOrEqual(before + 5);
  await page.getByRole("button", { name: "Yangilash" }).click();
  await expect(page.getByText(lanIP(), { exact: true }).or(page.getByText("127.0.0.1", { exact: true })).first()).toBeVisible();
});

test("a blocked request is listed under the blocked requests", async ({ page }) => {
  const domain = "e2e-stats-blocked.example";
  await api(page, "DELETE", `/squid/blacklist/${domain}`);
  expect((await api(page, "POST", "/squid/blacklist", { domain })).status).toBe(200);
  await expect.poll(() => viaSquid(`http://${domain}/x`), { timeout: 15_000 }).toBe("403");

  await expect
    .poll(async () => JSON.stringify((await api(page, "GET", "/monitor/denied?limit=20")).json), { timeout: 30_000, intervals: [2000] })
    .toContain(domain);
  await page.goto("/stats");
  await expect(page.getByText("So'nggi bloklangan so'rovlar")).toBeVisible();
  await expect(page.locator("table", { hasText: domain }).first()).toBeVisible();
  await api(page, "DELETE", `/squid/blacklist/${domain}`);
});

test("log files can be listed, opened and rotated", async ({ page }) => {
  await page.goto("/stats");
  await expect(page.getByText("Log fayllar")).toBeVisible();
  await page.getByRole("button", { name: /access\.log/ }).click();
  await expect(page.locator("pre")).toContainText(/\d+\.\d+/); // squid's native log lines start with a timestamp
  await page.getByRole("button", { name: /cache\.log/ }).click();
  await expect(page.locator("pre")).toBeVisible();

  await page.getByRole("button", { name: "Aylantirish" }).click();
  await expect(page.getByText("Loglarni aylantirish")).toBeVisible();
  await confirmDialog(page, "Aylantirish");
  await seeToast(page, "Loglar aylantirildi");
});

test("the live log page connects and shows a request as it happens", async ({ page }) => {
  await page.goto("/logs");
  await expect(page.getByText("Ulangan")).toBeVisible();
  // squid does not log the query string, so the marker goes into the path
  const marker = `e2e-live-${Date.now()}`;
  viaSquid(`http://${lanIP()}:8080/api/${marker}`);
  await expect(page.getByText(marker)).toBeVisible({ timeout: 15_000 });
});
