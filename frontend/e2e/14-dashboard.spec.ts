import { execSync } from "node:child_process";
import * as os from "node:os";
import { AUTH_FILE, api, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

function lanIP(): string {
  for (const list of Object.values(os.networkInterfaces())) {
    for (const i of list ?? []) if (i.family === "IPv4" && !i.internal) return i.address;
  }
  throw new Error("no network address");
}
/** A real request through squid: the HTTP status, or "no-proxy" when squid is not answering at all. */
function viaSquid(): string {
  try {
    return execSync(`curl -s -m 8 -o /dev/null -x http://127.0.0.1:3128 -w '%{http_code}' http://${lanIP()}:8080/api/health`).toString();
  } catch {
    return "no-proxy";
  }
}
const btn = (page: import("@playwright/test").Page, name: string) => page.getByRole("button", { name, exact: true });
const svc = async (page: import("@playwright/test").Page) => JSON.stringify((await api(page, "GET", "/squid/service")).json);

test("the dashboard shows the live state of squid", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Ishlamoqda", { exact: true }).first()).toBeVisible();
  await expect(btn(page, "Ishga tushirish")).toBeDisabled(); // already running
  await expect(btn(page, "To'xtatish")).toBeEnabled();
  await expect(btn(page, "Qayta ishga tushirish")).toBeEnabled();
  await expect(btn(page, "Reconfigure")).toBeEnabled();
  await expect(page.getByText("LAN qurilmalari")).toBeVisible();
  expect(viaSquid()).toBe("200");
});

test("reconfigure re-reads the configuration and the proxy answers again", async ({ page }) => {
  await page.goto("/");
  await btn(page, "Reconfigure").click();
  await seeToast(page, "Squid konfiguratsiyasi qayta yuklandi");
  await expect(page.getByText("Ishlamoqda", { exact: true }).first()).toBeVisible();
  // squid re-opens its listening sockets while it re-reads a large configuration: a few seconds at most
  await expect.poll(viaSquid, { timeout: 30_000 }).toBe("200");
});

test("stopping asks first; cancelling keeps squid up", async ({ page }) => {
  await page.goto("/");
  await btn(page, "To'xtatish").click();
  await expect(page.getByText("Squid'ni to'xtatish")).toBeVisible();
  await expect(page.getByText(/ulanishi uziladi/)).toBeVisible();
  await page.locator(".fixed.inset-0").getByRole("button", { name: /Bekor qilish/ }).click();
  await expect(page.getByText("Squid'ni to'xtatish")).toHaveCount(0);
  expect(await svc(page)).toMatch(/active|running/i);
  expect(viaSquid()).toBe("200");
});

test("stop really stops the proxy and start brings it back", async ({ page }) => {
  await page.goto("/");
  await btn(page, "To'xtatish").click();
  await page.locator(".fixed.inset-0").getByRole("button", { name: "To'xtatish" }).click();
  await expect(page.getByText("To'xtagan", { exact: true }).first()).toBeVisible({ timeout: 60_000 });
  await expect(btn(page, "Ishga tushirish")).toBeEnabled();
  await expect(btn(page, "To'xtatish")).toBeDisabled();
  await expect(btn(page, "Reconfigure")).toBeDisabled();
  expect(viaSquid()).toBe("no-proxy"); // nothing answers on the proxy port any more

  await btn(page, "Ishga tushirish").click();
  await expect(page.getByText("Ishlamoqda", { exact: true }).first()).toBeVisible({ timeout: 60_000 });
  await expect.poll(viaSquid, { timeout: 30_000 }).toBe("200");
});

test("restart goes through a confirmation and the proxy is back afterwards", async ({ page }) => {
  await page.goto("/");
  await btn(page, "Qayta ishga tushirish").click();
  await page.locator(".fixed.inset-0").getByRole("button", { name: "Qayta ishga tushirish" }).click();
  await expect(page.getByText("Ishlamoqda", { exact: true }).first()).toBeVisible({ timeout: 60_000 });
  await expect.poll(viaSquid, { timeout: 40_000 }).toBe("200");
});

// KNOWN PROBLEM, found by this test in a real browser: on a server installed with deploy/install.sh
// the switch answers 422 and nothing changes. The unit runs with ProtectSystem=full, so /etc is
// read-only for the sudo child, and "systemctl enable squid" cannot write its symlinks under
// /etc/systemd/system and /etc/rc*.d. Start/stop/restart are unaffected (they go through D-Bus).
// Fixing it means widening ReadWritePaths in the unit, a security decision left to the owner.
test.fixme("start on boot: the switch really enables and disables squid's autostart", async ({ page }) => {
  await page.goto("/");
  const enabled = async () => {
    const j = (await api(page, "GET", "/squid/service")).json;
    return j.service?.enabled ?? j.enabled;
  };
  const before = await enabled();
  const boot = page.getByText("Tizim yoqilganda avtomatik ishga tushirish", { exact: true }).locator("xpath=following-sibling::*[1]");
  await expect(boot).toHaveCount(1);
  await boot.click();
  await expect.poll(enabled).not.toBe(before);
  await boot.click();
  await expect.poll(enabled).toBe(before);
});

test("LAN access switch changes the real setting and can be put back", async ({ page }) => {
  await page.goto("/");
  const lanBefore = (await api(page, "GET", "/squid/lan-access")).json.allowed;
  const lan = page.getByText("Yoqilgan bo'lsa, lokal tarmoqdagi boshqa qurilmalar ham proxy'dan foydalana oladi").locator("xpath=following-sibling::*[1]//button");
  await expect(lan).toHaveCount(1);
  await lan.click();
  await expect.poll(async () => (await api(page, "GET", "/squid/lan-access")).json.allowed).toBe(!lanBefore);
  await seeToast(page, /LAN qurilmalari uchun proxy ruxsati/);
  await expect(page.getByText(lanBefore ? "Faqat shu kompyuter" : "Ruxsat berilgan", { exact: true })).toBeVisible();
  await lan.click();
  await expect.poll(async () => (await api(page, "GET", "/squid/lan-access")).json.allowed).toBe(lanBefore);
  await expect.poll(viaSquid, { timeout: 30_000 }).toBe("200");
});
