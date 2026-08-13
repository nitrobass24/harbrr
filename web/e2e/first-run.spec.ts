import { expect, test } from "@playwright/test"

// The webServer (playwright.config.ts) wipes .e2e-data before boot, so the app
// starts unconfigured. This walks the whole first-run path in order — setup
// wizard → login → authenticated dashboard → log out — one browser journey
// covering auth guards, session cookies, and the embedded bundle end to end.
test("first run: setup, login, dashboard, log out", async ({ page }) => {
  await page.goto("/")
  await expect(page).toHaveURL(/\/setup/)

  // Create the admin account.
  await page.getByLabel("Username").fill("admin")
  await page.getByLabel("Password", { exact: true }).fill("password123")
  await page.getByLabel("Confirm password").fill("password123")
  await page.getByRole("button", { name: "Create account" }).click()

  // Setup hands off to login; sign in with the account just created.
  await expect(page).toHaveURL(/\/login/)
  await page.getByLabel("Username").fill("admin")
  await page.getByLabel("Password", { exact: true }).fill("password123")
  await page.getByRole("button", { name: "Sign in" }).click()

  // Landed on the authenticated dashboard.
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible()

  // Log out returns to login, and the auth guard keeps / off-limits.
  await page.getByRole("button", { name: "Log out" }).click()
  await expect(page).toHaveURL(/\/login/)
  await page.goto("/")
  await expect(page).toHaveURL(/\/login/)
})

test("wrong password is rejected", async ({ page }) => {
  await page.goto("/login")
  await page.getByLabel("Username").fill("admin")
  await page.getByLabel("Password", { exact: true }).fill("wrong-password")
  await page.getByRole("button", { name: "Sign in" }).click()
  await expect(page.getByRole("alert")).toHaveText("Wrong username or password.")
})
