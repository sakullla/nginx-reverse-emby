const { test, expect } = require('@playwright/test')
const { login } = require('./helpers')

test('cycles theme and persists it after reload', async ({ page }) => {
  await login(page)

  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await page.getByTestId('theme-toggle').click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')

  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')

  await page.getByTestId('theme-toggle').click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'anime')
})

test('copies the join command and shows different apply feedback for local and remote agents', async ({ page }) => {
  await login(page)

  const joinCommand = (await page.getByTestId('join-command').innerText()).trim()
  await page.getByTestId('copy-join-command').click()
  await expect(page.getByTestId('status-message')).toBeVisible()

  const clipboardText = await page.evaluate(() => navigator.clipboard.readText())
  expect(clipboardText.trim()).toBe(joinCommand)
  expect(clipboardText).toContain('--master-url http://127.0.0.1:4173')

  await page.getByTestId('apply-config-button').click()
  await expect(page.getByTestId('status-message')).toHaveClass(/success/)

  await page.getByTestId('agent-card-edge-1').click()
  await page.getByTestId('apply-config-button').click()
  await expect(page.getByTestId('status-message')).toHaveClass(/info/)
})


test('toggles rule view mode and keeps it after reload', async ({ page }) => {
  await login(page)

  await expect(page.getByTestId('rules-layout')).toHaveClass(/view-grid/)
  await page.getByTestId('view-mode-toggle').click()
  await expect(page.getByTestId('rules-layout')).toHaveClass(/view-list/)

  await page.reload()
  await expect(page.getByTestId('rules-layout')).toHaveClass(/view-list/)
  await page.getByTestId('view-mode-toggle').click()
  await expect(page.getByTestId('rules-layout')).toHaveClass(/view-grid/)
})
