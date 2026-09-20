import * as http from "node:http";
import * as os from "node:os";
import { AUTH_FILE, api, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const PORT = 18099;
const received: any[] = [];
let server: http.Server;

function lanIP(): string {
  for (const list of Object.values(os.networkInterfaces())) {
    for (const i of list ?? []) if (i.family === "IPv4" && !i.internal) return i.address;
  }
  throw new Error("no network address");
}
const HOOK = () => `http://${lanIP()}:${PORT}/hook-secret-path`;
const waitFor = async (fn: () => boolean, ms = 15_000) => {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (fn()) return true;
    await new Promise((r) => setTimeout(r, 300));
  }
  return fn();
};
const switchOf = (page: import("@playwright/test").Page, label: string) => page.getByRole("switch", { name: label, exact: true });
const WEBHOOK = "Webhook (Slack, Discord, Mattermost…)";
/** Puts a switch into the wanted state whatever it was before. */
async function setSwitch(page: import("@playwright/test").Page, label: string, on: boolean) {
  const sw = switchOf(page, label);
  if ((await sw.getAttribute("aria-checked")) !== String(on)) await sw.click();
  await expect(sw).toHaveAttribute("aria-checked", String(on));
}
const save = (page: import("@playwright/test").Page) => page.getByRole("button", { name: "Saqlash", exact: true }).click();
const webhookInput = (page: import("@playwright/test").Page) => page.getByPlaceholder(/hooks\.example\.com|saqlangan \(/);

test.beforeAll(async () => {
  server = http.createServer((req, res) => {
    let body = "";
    req.on("data", (d) => (body += d));
    req.on("end", () => {
      try {
        received.push(JSON.parse(body));
      } catch {
        received.push({ raw: body });
      }
      res.writeHead(200).end("ok");
    });
  });
  await new Promise<void>((resolve) => server.listen(PORT, "0.0.0.0", resolve));
});
test.afterAll(async () => {
  await new Promise((r) => server.close(r));
});

test("the page shows the conditions being watched; a test with no channel says so", async ({ page }) => {
  await page.goto("/alerts");
  await expect(page.getByRole("heading", { name: "Ogohlantirishlar" }).first()).toBeVisible();
  await expect(page.getByText("Holat", { exact: true })).toBeVisible();
  await expect(page.getByText("Nimalar haqida xabar berilsin")).toBeVisible();
  await expect(page.getByText("Kanallar", { exact: true })).toBeVisible();
  for (const s of ["Squid to'xtab qolsa", "Disk to'lib borsa", "Bloklashlar ko'paysa", "Server xatolari ko'paysa"]) {
    await expect(page.getByText(s).first()).toBeVisible();
  }
  // disable whatever an earlier run left on, so "no channel" is really the state
  if (await switchOf(page, "Webhook (Slack, Discord, Mattermost…)").getAttribute("aria-checked").then((v) => v === "true")) {
    await switchOf(page, "Webhook (Slack, Discord, Mattermost…)").click();
    await save(page);
    await seeToast(page, "Saqlandi");
  }
  await expect(page.getByText("Hech bir kanal yoqilmagan")).toBeVisible();

  await page.getByRole("button", { name: "Sinov xabari" }).click();
  await expect(page.getByText(/Sinov xabari yuborildi/)).toHaveCount(0); // it did not pretend to send
  expect(received).toEqual([]);
});

test("a malformed webhook address is refused; one aimed at this machine or metadata is saved but never contacted", async ({ page }) => {
  received.length = 0;
  await page.goto("/alerts");
  const hostBefore = (await api(page, "GET", "/monitor/alerts")).json.config.webhook.host;
  await setSwitch(page, WEBHOOK, true);

  // wrong scheme / not an address: refused when saving, with the reason under the field
  for (const bad of ["ftp://example.com/x", "not a url"]) {
    await webhookInput(page).fill(bad);
    await save(page);
    await expect(page.getByText("Saqlandi", { exact: true })).toHaveCount(0);
    await expect(page.getByText(/valid http/i).first()).toBeVisible();
    expect((await api(page, "GET", "/monitor/alerts")).json.config.webhook.host).toBe(hostBefore);
  }

  // this machine and cloud metadata pass the format check, but the sender refuses to dial them
  for (const [bad, host] of [["http://127.0.0.1:9/x", "127.0.0.1:9"], ["http://169.254.169.254/latest", "169.254.169.254"]]) {
    await webhookInput(page).fill(bad);
    await save(page);
    await seeToast(page, "Saqlandi");
    await page.getByRole("button", { name: "Sinov xabari" }).click();
    await expect(page.getByText(/webhook: .*(not a public or LAN address|refus)/i).first()).toBeVisible();
    await expect(page.getByText(/Sinov xabari yuborildi/)).toHaveCount(0);
    expect((await api(page, "GET", "/monitor/alerts")).json.config.webhook.host).toBe(host);
  }
  expect(received).toEqual([]);

  // thresholds outside their range are refused
  await setSwitch(page, WEBHOOK, false);
  const percent = page.getByLabel("Chegara, %");
  await percent.fill("150");
  await save(page);
  await expect(page.getByText("Saqlandi", { exact: true })).toHaveCount(0);
  await expect(page.getByText(/between 50 and 99/).first()).toBeVisible();
  await page.reload();
});

test("a real webhook receives the test message; its address is never shown again", async ({ page }) => {
  received.length = 0;
  await page.goto("/alerts");
  await setSwitch(page, WEBHOOK, true);
  await webhookInput(page).fill(HOOK());
  await save(page);
  await seeToast(page, "Saqlandi");

  // the secret path is not echoed back anywhere on the page or in the API
  await expect(page.getByPlaceholder(/saqlangan \(/)).toBeVisible();
  expect(await page.content()).not.toContain("hook-secret-path");
  expect(JSON.stringify((await api(page, "GET", "/monitor/alerts")).json)).not.toContain("hook-secret-path");
  await expect(page.getByText("Hech bir kanal yoqilmagan")).toHaveCount(0);

  await page.getByRole("button", { name: "Sinov xabari" }).click();
  await seeToast(page, /Sinov xabari yuborildi: webhook/);
  expect(await waitFor(() => received.some((m) => m.kind === "test"))).toBe(true);

  // and it is in the history
  await expect(page.locator("tbody tr", { hasText: "Sinov" }).first()).toBeVisible();
});

test("a real outage is detected, sent out, and the recovery is sent too", async ({ page }) => {
  received.length = 0;
  await page.goto("/alerts");
  const squidRow = page.locator("li", { hasText: "Squid to'xtab qolishi" }).first();
  await expect(squidRow).toContainText("normal");

  expect((await api(page, "POST", "/squid/service/stop")).status).toBe(200);
  await expect.poll(async () => JSON.stringify((await api(page, "GET", "/squid/service")).json), { timeout: 60_000 }).toMatch(/inactive|stopped|dead|failed/i);

  // "two checks in a row" before it is reported
  await page.getByRole("button", { name: "Hozir tekshirish" }).click();
  await page.waitForTimeout(800);
  await page.getByRole("button", { name: "Hozir tekshirish" }).click();
  await seeToast(page, /yangi hodisa/);
  await expect(squidRow).toContainText("muammo");
  expect(await waitFor(() => received.some((m) => m.key === "squid_down" && m.kind === "firing"))).toBe(true);
  await expect(page.locator("tbody tr", { hasText: "Muammo" }).first()).toBeVisible();

  expect((await api(page, "POST", "/squid/service/start")).status).toBe(200);
  await expect.poll(async () => JSON.stringify((await api(page, "GET", "/squid/service")).json), { timeout: 60_000 }).toMatch(/active|running/i);
  await page.getByRole("button", { name: "Hozir tekshirish" }).click();
  await expect(squidRow).toContainText("normal", { timeout: 15_000 });
  expect(await waitFor(() => received.some((m) => m.key === "squid_down" && m.kind === "resolved"))).toBe(true);
  await page.reload();
  await expect(page.locator("tbody tr", { hasText: "Hal bo'ldi" }).first()).toBeVisible();
});

test("the webhook is switched off again at the end", async ({ page }) => {
  await page.goto("/alerts");
  await setSwitch(page, WEBHOOK, false);
  await save(page);
  await seeToast(page, "Saqlandi");
  await expect(page.getByText("Hech bir kanal yoqilmagan")).toBeVisible();
});
