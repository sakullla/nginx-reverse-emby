const { test, expect } = require('@playwright/test')
const { openFreshApp, login } = require('./helpers')

test('shows a validation error when token is empty', async ({ page }) => {
  await openFreshApp(page)

  await page.getByTestId('login-submit').click()

  await expect(page.getByTestId('login-error')).toBeVisible()
})

test('logs in, survives reload, and logs out', async ({ page }) => {
  await login(page)

  await page.reload()
  await expect(page.getByTestId('logout-button')).toBeVisible()

  await page.getByTestId('logout-button').click()
  await expect(page.getByTestId('login-screen')).toBeVisible()
})
