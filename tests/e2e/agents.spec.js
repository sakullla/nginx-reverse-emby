const { test, expect } = require('@playwright/test')
const { login } = require('./helpers')

test('lists agents, refreshes them, switches nodes, and removes a remote agent', async ({ page }) => {
  await login(page)

  await expect(page.getByTestId('agent-card-local')).toBeVisible()
  await expect(page.getByTestId('agent-card-edge-1')).toBeVisible()
  await expect(page.getByTestId('agent-card-local').getByTestId('remove-agent')).toHaveCount(0)

  await page.getByTestId('refresh-agents').click()
  await expect(page.getByTestId('agent-card-edge-1')).toBeVisible()

  await page.getByTestId('agent-card-edge-1').click()
  await expect(page.getByTestId('agent-card-edge-1')).toHaveClass(/active/)
  await expect(page.getByText('https://jellyfin.example.com')).toBeVisible()
  await expect(page.getByText('http://192.168.1.11:8096')).toBeVisible()

  page.once('dialog', (dialog) => dialog.accept())
  await page.getByTestId('agent-card-edge-1').getByTestId('remove-agent').click()

  await expect(page.getByTestId('agent-card-edge-1')).toHaveCount(0)
  await expect(page.getByTestId('agent-card-local')).toHaveClass(/active/)
})
