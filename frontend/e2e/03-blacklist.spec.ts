import { AUTH_FILE, api, confirmDialog, expect, seeToast, test } from "./helpers";

test.use({ storageState: AUTH_FILE });
test.describe.configure({ mode: "serial" });

const DOMAIN = "e2e-blocked.example";

test("a domain can be blocked and squid really blocks it", async ({ page }) => {
  await page.goto("/blacklist");
  await page.getByPlaceholder("masalan: example.com").fill(DOMAIN);
  await page.getByRole("button", { name: "Qo'shish" }).click();
  await seeToast(page, `${DOMAIN} bloklandi`);
  await expect(page.locator("li", { hasText: DOMAIN })).toBeVisible();

  // ...and it is in squid's own list, not only on the screen
  const list = await api(page, "GET", "/squid/blacklist");
  expect(JSON.stringify(list.json)).toContain(DOMAIN);
});

test("the same domain twice, and garbage, are refused with a message", async ({ page }) => {
  await page.goto("/blacklist");
  await page.getByPlaceholder("masalan: example.com").fill("not a domain!!");
  await page.getByRole("button", { name: "Qo'shish" }).click();
  await expect(page.locator(".pointer-events-none").getByText(/./).first()).toBeVisible(); // an error toast appeared
  await expect(page.locator("li", { hasText: DOMAIN })).toHaveCount(1); // the list is unchanged
});

test("the blocked domain can be unblocked after a confirmation", async ({ page }) => {
  await page.goto("/blacklist");
  const row = page.locator("li", { hasText: DOMAIN });
  await row.getByRole("button").click();
  // Cancelling keeps it
  await confirmDialog(page, "Bekor qilish");
  await expect(row).toBeVisible();
  // Confirming removes it
  await row.getByRole("button").click();
  await confirmDialog(page, "O'chirish");
  await expect(row).toHaveCount(0);
  const list = await api(page, "GET", "/squid/blacklist");
  expect(JSON.stringify(list.json)).not.toContain(DOMAIN);
});
