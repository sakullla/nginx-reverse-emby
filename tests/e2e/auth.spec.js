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


test('shows an error for invalid token and stays on the login screen', async ({ page }) => {
  await page.goto('/')

  await page.getByTestId('login-token-input').fill('wrong-token')
  await page.getByTestId('login-submit').click()

  await expect(page.getByTestId('login-screen')).toBeVisible()
  await expect(page.getByTestId('status-message')).toBeVisible()
})

test('clears expired token on 401 during auth check and returns to login', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('panel_token', 'expired-401')
    localStorage.setItem('panel_dev_mock_flags', JSON.stringify({ force401OnVerify: true }))
  })

  await page.goto('/')

  await expect(page.getByTestId('login-screen')).toBeVisible()
  await expect(page.getByTestId('status-message')).toBeVisible()
  await expect.poll(async () => page.evaluate(() => localStorage.getItem('panel_token'))).toBeNull()
})
