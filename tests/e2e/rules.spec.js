const { test, expect } = require('@playwright/test')
const { login, ruleCardByText } = require('./helpers')

test('adds, edits, filters, searches, toggles, and deletes a local rule', async ({ page }) => {
  await login(page)

  const frontendUrl = 'https://series.example.com'
  const backendUrl = 'http://10.0.0.20:8096'
  const updatedBackendUrl = 'http://10.0.0.30:8096'

  await page.getByTestId('add-rule-button').click()
  await expect(page.getByTestId('rule-form')).toBeVisible()

  await page.getByTestId('frontend-url-input').fill(frontendUrl)
  await page.getByTestId('backend-url-input').fill(backendUrl)
  await page.getByTestId('tag-input').fill('series')
  await page.getByTestId('tag-input').press('Enter')
  await page.getByTestId('rule-submit').click()

  const newRule = ruleCardByText(page, frontendUrl)
  await expect(newRule).toBeVisible()
  await expect(newRule).toContainText(backendUrl)

  await newRule.getByTestId('rule-tag').filter({ hasText: 'series' }).click()
  await expect(page.locator('[data-testid^="rule-item-"]')).toHaveCount(1)
  await expect(newRule).toBeVisible()
  await newRule.getByTestId('rule-tag').filter({ hasText: 'series' }).click()
  await expect(page.locator('[data-testid^="rule-item-"]')).toHaveCount(2)

  await newRule.getByTestId('rule-edit').click()
  await expect(page.getByTestId('rule-form')).toBeVisible()
  await page.getByTestId('backend-url-input').fill(updatedBackendUrl)
  await page.getByTestId('rule-submit').click()
  await expect(newRule).toContainText(updatedBackendUrl)

  await page.getByTestId('rules-search-input').fill('series.example.com')
  await expect(ruleCardByText(page, frontendUrl)).toBeVisible()
  await expect(page.locator('[data-testid^="rule-item-"]')).toHaveCount(1)
  await page.getByTestId('rules-search-input').fill('does-not-exist')
  await expect(page.locator('[data-testid^="rule-item-"]')).toHaveCount(0)
  await page.getByTestId('rules-search-input').fill('')
  await expect(page.locator('[data-testid^="rule-item-"]')).toHaveCount(2)

  await newRule.getByTestId('rule-toggle').click()
  await expect(ruleCardByText(page, frontendUrl)).toHaveClass(/is-disabled/)

  await ruleCardByText(page, frontendUrl).getByTestId('rule-delete').click()
  await expect(page.getByTestId('modal-overlay')).toBeVisible()
  await page.getByTestId('modal-confirm').click()
  await expect(page.locator('[data-testid^="rule-item-"]').filter({ hasText: frontendUrl })).toHaveCount(0)
})
