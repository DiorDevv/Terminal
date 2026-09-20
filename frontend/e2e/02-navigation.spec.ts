import { AUTH_FILE, expect, test } from "./helpers";

test.use({ storageState: AUTH_FILE });

// Every entry of the side menu opens its page (and only that page's content).
const pages: [string, string, RegExp][] = [
  ["Dashboard", "/", /^Dashboard$/],
  ["Bloklangan domenlar", "/blacklist", /^Bloklangan domenlar$/],
  ["Kirish qoidalari", "/access", /Kirish qoidalari/],
  ["Blocklist manbalari", "/blocklists", /Blocklist/],
  ["Vaqt cheklovlari", "/restrictions", /Vaqt asosidagi cheklovlar/],
  ["IP guruhlari", "/groups", /^IP guruhlari$/],
  ["Proxy foydalanuvchilari", "/users", /^Proxy foydalanuvchilari$/],
  ["Cheklovlar", "/limits", /^Cheklovlar$/],
  ["Statistika", "/stats", /^Statistika$/],
  ["Loglar", "/logs", /Jonli loglar/],
  ["Squid sozlamalari", "/squid-settings", /Squid sozlamalari/],
  ["Konfiguratsiya", "/config", /squid\.conf/],
  ["Config tarixi", "/history", /Config tarixi|tarix/i],
  ["Panel foydalanuvchilari", "/panel-users", /Panel foydalanuvchilari/],
  ["Audit log", "/audit", /Audit/],
  ["Ogohlantirishlar", "/alerts", /^Ogohlantirishlar$/],
  ["Sozlamalar", "/settings", /^Sozlamalar$/],
];

test("every side-menu entry opens its page", async ({ page }) => {
  await page.goto("/");
  for (const [label, url, heading] of pages) {
    await page.locator("nav").getByRole("link", { name: label, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(url === "/" ? "/$" : url + "$"));
    await expect(page.locator("main h2").first()).toHaveText(heading);
  }
});

test("the dashboard shows the state of squid", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Squid ishlamoqda").first()).toBeVisible();
});

test("on a phone the menu is a drawer that opens and navigates", async ({ page }) => {
  await page.setViewportSize({ width: 400, height: 800 });
  await page.goto("/");
  await expect(page.locator("nav")).not.toBeInViewport();
  await page.getByRole("button", { name: "Menyuni ochish" }).click();
  await expect(page.locator("nav")).toBeInViewport();
  await page.locator("nav").getByRole("link", { name: "Statistika", exact: true }).click();
  await expect(page).toHaveURL(/\/stats$/);
  await expect(page.locator("nav")).not.toBeInViewport(); // the drawer closes after choosing
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow, "no sideways scrolling on a phone").toBeLessThanOrEqual(0);
});
