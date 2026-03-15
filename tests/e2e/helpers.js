const { expect } = require('@playwright/test')

async function openFreshApp(page) {
  await page.goto('/')
}

async function login(page, token = 'admin') {
  await openFreshApp(page)
  await expect(page.getByTestId('login-screen')).toBeVisible()
  await page.getByTestId('login-token-input').fill(token)
  await page.getByTestId('login-submit').click()
  await expect(page.getByTestId('logout-button')).toBeVisible()
  await expect(page.getByTestId('agent-card-local')).toBeVisible()
  await expect(page.getByTestId('add-rule-button')).toBeEnabled()
  await expect(page.locator('[data-testid^="rule-item-"]').first()).toBeVisible()
}

function ruleCardByText(page, text) {
  return page.locator('[data-testid^="rule-item-"]').filter({ hasText: text }).first()
}

module.exports = {
  openFreshApp,
  login,
  ruleCardByText
}
