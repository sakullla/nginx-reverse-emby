/** Visual capture of the panel against the Vite dev server. Synthetic fixtures; never acceptance evidence. */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const frontendRoot = fileURLToPath(new URL('..', import.meta.url))
const outDir = process.env.NRE_CAPTURE_OUT
  ?? path.join(frontendRoot, 'docs', 'verification', 'ui')
const baseURL = (process.env.NRE_CAPTURE_URL ?? 'http://localhost:5173').replace(/\/$/, '')
const themes = (process.env.NRE_CAPTURE_THEMES ?? 'sakura-day,sakura-night').split(',').map((item) => item.trim()).filter(Boolean)
const widths = (process.env.NRE_CAPTURE_WIDTHS ?? '1360,900,390').split(',').map((item) => Number(item.trim())).filter((item) => item > 0)

fs.mkdirSync(outDir, { recursive: true })

const plugins = [
  {
    plugin_id: 'official.emby-helper',
    name: 'Emby 助手',
    version: '1.4.2',
    current_lifecycle: 'active',
    runtime_kind: 'rpc-service',
    active_source_kind: 'official',
    active_source_risk_label: '低'
  },
  {
    plugin_id: 'official.waf',
    name: '网站防火墙',
    version: '2.1.0',
    current_lifecycle: 'installed',
    runtime_kind: 'wasm-policy',
    active_source_kind: 'official',
    active_source_risk_label: '低'
  }
]

const sources = [
  {
    id: 'official',
    name: '官方市场',
    kind: 'official',
    purpose: 'market',
    risk_label: 'official',
    url: 'https://example.com/official/market.git',
    ref_kind: 'tag',
    ref_name: 'v1.4.2',
    last_result: 'succeeded',
    last_error: '',
    last_completed_at: '2026-10-01T08:00:00Z'
  },
  {
    id: 'community',
    name: '社区仓库',
    kind: 'custom',
    purpose: 'plugin',
    risk_label: 'community',
    url: 'https://example.com/community/plugins.git',
    ref_kind: 'branch',
    ref_name: 'main',
    last_result: 'succeeded',
    last_error: '',
    last_completed_at: '2026-09-28T03:12:00Z'
  }
]

const catalog = {
  official: [
    { id: 'official.emby-helper', name: 'Emby 助手', version: '1.4.2', sha256: 'preview-emby-helper', description: '媒体访问入口' },
    { id: 'official.waf', name: '网站防火墙', version: '2.1.0', sha256: 'preview-waf', description: '入口请求检查' }
  ],
  community: [
    { id: 'community.ddns', name: '动态域名', version: '0.9.1', sha256: 'preview-ddns', description: '公网地址自动更新' }
  ]
}

function pluginDetail(id) {
  const summary = plugins.find((item) => item.plugin_id === id) ?? {
    plugin_id: id,
    name: id,
    version: '1.0.0',
    current_lifecycle: 'installed'
  }
  const deployed = summary.plugin_id === 'official.emby-helper'
  return {
    plugin: summary,
    package: {
      version: summary.version,
      manifest: {
        name: summary.name,
        description: deployed ? '把 Emby 发布到选定节点。' : '检查进入站点的请求。',
        http_backend_providers: deployed ? [{ id: 'default', display_name: '默认' }] : []
      }
    },
    instances: deployed ? [{ id: 'inst-emby', resource_group_id: 'default', targets: ['local'] }] : [],
    agent_statuses: deployed ? [{ instance_id: 'inst-emby', agent_id: 'local', runtime_state: 'active' }] : [],
    published_entries: deployed ? [{
      rule_id: 12,
      agent_id: 'local',
      instance_id: 'inst-emby',
      frontend_url: 'https://media.example.com',
      enabled: true,
      accessible: true
    }] : []
  }
}

function json(route, body, status = 200) {
  return route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body)
  })
}

async function fulfillPanelApi(route) {
  const request = route.request()
  const url = new URL(request.url())
  const pathname = url.pathname.replace(/^\/panel-api/, '') || '/'
  if (pathname === '/agents/monitor-stream') {
    return route.fulfill({
      status: 200,
      contentType: 'application/x-ndjson',
      body: `${JSON.stringify({ type: 'snapshot', payload: { agents: [] } })}\n`
    })
  }
  if (pathname === '/auth/me') {
    return json(route, {
      actor: {
        username: 'admin',
        display_name: '管理员',
        permissions: ['*'],
        visible_resource_groups: ['default'],
        bootstrap: false
      }
    })
  }
  if (pathname === '/auth/verify') return json(route, { ok: true })
  if (pathname === '/auth/logout' || pathname === '/auth/login') return json(route, { ok: true })
  if (pathname === '/plugin-ui-routes') return json(route, { routes: [] })
  if (pathname === '/plugin-resource-groups') {
    return json(route, { groups: [{ id: 'default', name: '默认组', description: '' }] })
  }
  if (pathname === '/plugins' && request.method() === 'GET') return json(route, { plugins })
  if (pathname === '/plugins/package-detail') {
    const selection = request.postDataJSON() || {}
    const entry = Object.values(catalog).flat().find((item) => item.id === selection.plugin_id)
    return json(route, {
      package: {
        digest: entry?.sha256,
        version: entry?.version,
        manifest: { id: entry?.id, name: entry?.name },
        permissions: ['http.outbound', 'secret.use', 'dns.manage']
      }
    })
  }
  if (/^\/plugins\/[^/]+\/operations$/.test(pathname)) return json(route, { operations: [] })
  const pluginMatch = pathname.match(/^\/plugins\/([^/]+)$/)
  if (pluginMatch && request.method() === 'GET') {
    return json(route, pluginDetail(decodeURIComponent(pluginMatch[1])))
  }
  if (pathname === '/marketplace/sources' && request.method() === 'GET') return json(route, { sources })
  const entriesMatch = pathname.match(/^\/marketplace\/sources\/([^/]+)\/entries$/)
  if (entriesMatch) {
    const id = decodeURIComponent(entriesMatch[1])
    return json(route, { entries: catalog[id] ?? [], direct_plugin: null })
  }
  if (pathname === '/revision-events') return json(route, { events: [], next_cursor: 0 })
  if (pathname.startsWith('/operations/')) return json(route, { operation_id: 'op-1', apply_status: 'applied', completed_at: '2026-10-01T08:00:00Z', agents: [] })
  if (pathname.startsWith('/pki/')) {
    return json(route, { message: 'preview' }, 503)
  }
  if (request.method() === 'GET') return json(route, {})
  return json(route, { ok: true })
}

async function launchBrowser() {
  const channel = process.env.NRE_CAPTURE_CHANNEL ?? 'msedge'
  try {
    return await chromium.launch({ channel, headless: true })
  } catch (error) {
    console.warn(`channel ${channel} unavailable (${error.message}); falling back to bundled Chromium`)
    return await chromium.launch({ headless: true })
  }
}

const failures = []
const browser = await launchBrowser()

for (const width of widths) {
  for (const theme of themes) {
    const context = await browser.newContext({ viewport: { width, height: 1000 }, deviceScaleFactor: 1, hasTouch: width < 1024 })
    await context.route('**/panel-api/**', fulfillPanelApi)
    await context.addInitScript((initialTheme) => {
      localStorage.setItem('panel_token', 'admin')
      localStorage.setItem('theme', initialTheme)
      localStorage.setItem('sidebar_collapsed', 'false')
      localStorage.setItem('sidebar_open_groups', JSON.stringify(['流量管理', '基础设施', '插件']))
    }, theme)
    const page = await context.newPage()
    page.on('pageerror', (error) => failures.push({ theme, width, message: error.message }))

    async function settle() {
      // Let lazy route modules mount before checking their loading indicators.
      await page.waitForTimeout(500)
      await page.waitForFunction(() => {
        const busy = document.querySelectorAll('.spinner, .skeleton, [class*="skeleton"]')
        return [...busy].every((el) => !el.getClientRects().length)
      }, undefined, { timeout: 8000 })
      await page.evaluate(() => document.fonts.ready)
    }

    async function chooseLocalAgent() {
      const all = page.getByRole('button', { name: '全部节点' })
      if (!(await all.count())) return
      await all.first().click()
      await page.getByRole('option', { name: /本机 Agent/ }).click()
    }

    async function capture(name) {
      await settle()
      await page.evaluate((activeTheme) => {
        document.documentElement.dataset.theme = activeTheme
      }, theme)
      const file = path.join(outDir, `${name}-${theme}-${width}.png`)
      await page.screenshot({ path: file, animations: 'disabled' })
      const over = await page.evaluate(() => [...document.querySelectorAll('main, section, article, input, textarea, select, button, table')]
        .filter((el) => el.getClientRects().length && el.getBoundingClientRect().right > window.innerWidth + 1)
        .slice(0, 8)
        .map((el) => ({ tag: el.tagName, text: (el.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 80) })))
      if (over.length) failures.push({ name, theme, width, over })
      const scrolled = await page.evaluate(() => {
        const el = document.querySelector('.app-shell .content')
        if (!el || el.scrollHeight <= el.clientHeight + 40) return false
        el.scrollTop = el.scrollHeight
        return true
      })
      if (scrolled) {
        await page.waitForTimeout(80)
        const endFile = path.join(outDir, `${name}-end-${theme}-${width}.png`)
        await page.screenshot({ path: endFile, animations: 'disabled' })
        await page.evaluate(() => {
          const el = document.querySelector('.app-shell .content')
          if (el) el.scrollTop = 0
        })
      }
      console.log(file)
    }

    async function open(urlPath, readyText) {
      await page.goto(`${baseURL}${urlPath}`, { waitUntil: 'domcontentloaded' })
      await page.getByText(readyText, { exact: false }).first().waitFor({ timeout: 20000 })
    }

    async function closeDialog() {
      await page.keyboard.press('Escape')
      await page.getByRole('dialog').waitFor({ state: 'hidden', timeout: 4000 }).catch(() => {})
    }

    const desktop = width >= 1024

    async function step(name, run) {
      try {
        await run()
      } catch (error) {
        failures.push({ name, theme, width, message: error.message })
        console.error(`FAIL ${name} ${theme} ${width}: ${error.message}`)
      }
    }

    await step('login', async () => {
      await page.goto(`${baseURL}/login`, { waitUntil: 'domcontentloaded' })
      await page.evaluate(() => localStorage.removeItem('panel_token'))
      await page.reload({ waitUntil: 'domcontentloaded' })
      await page.getByRole('heading', { name: 'Nginx Proxy' }).waitFor()
      await capture('login')
      await page.getByRole('button', { name: '连接' }).click()
      await page.getByRole('alert').waitFor()
      await capture('login-error')
      await page.evaluate((activeTheme) => {
        localStorage.setItem('panel_token', 'admin')
        localStorage.setItem('theme', activeTheme)
        localStorage.setItem('sidebar_open_groups', JSON.stringify(['流量管理', '基础设施', '插件']))
      }, theme)
    })

    await step('dashboard', async () => {
      await open('/', '集群概览')
      await capture('dashboard')
      if (desktop) {
        await page.getByRole('button', { name: '全局搜索' }).click()
        await page.getByRole('dialog', { name: '全局搜索' }).waitFor()
        await page.getByRole('combobox').fill('emby')
        await page.waitForTimeout(700)
        await capture('search')
        await closeDialog()
        await page.getByRole('button', { name: '账号' }).click()
        await page.getByRole('menu', { name: '账号' }).waitFor()
        await capture('account-menu')
        await page.keyboard.press('Escape')
        await page.locator('.theme-trigger').click()
        await page.getByText('选择主题').waitFor()
        await capture('theme-menu')
        await page.keyboard.press('Escape')
      } else {
        await page.getByRole('button', { name: '更多' }).click()
        await page.getByRole('menu').waitFor()
        await capture('mobile-more')
        await page.keyboard.press('Escape')
      }
    })

    await step('agents', async () => {
      await open('/agents', '节点管理')
      await capture('agents')
      if (!desktop) return
      await page.getByTitle('列表视图').click()
      await page.waitForTimeout(200)
      await capture('agents-list')
      await page.getByTitle('监控视图').click()
      await page.getByRole('button', { name: '加入节点' }).click()
      await page.getByRole('dialog').waitFor()
      await capture('agents-join')
      await closeDialog()
    })

    await step('agent-detail', async () => {
      await open('/agents/local', '本机 Agent')
      await capture('agent-detail')
    })

    await step('rules', async () => {
      await open('/rules', 'HTTP 规则')
      await capture('rules')
      await chooseLocalAgent()
      await page.getByRole('button', { name: '添加规则' }).waitFor()
      await capture('rules-agent')
      if (desktop) {
        await page.getByTitle('列表视图').click()
        await capture('rules-list')
      }
      await page.getByRole('button', { name: '添加规则' }).click()
      await page.getByRole('dialog').waitFor()
      await capture('rules-create')
      await closeDialog()
      await page.getByRole('button', { name: '筛选' }).click()
      await capture('rules-filter')
      await page.keyboard.press('Escape')
    })

    await step('l4', async () => {
      await open('/l4', 'L4 规则')
      await capture('l4')
      await chooseLocalAgent()
      await page.getByRole('button', { name: '添加 L4 规则' }).click()
      await page.getByRole('dialog').waitFor()
      await capture('l4-create')
      await closeDialog()
    })

    await step('certs', async () => {
      await open('/certs', '公网证书')
      await capture('certs')
      await chooseLocalAgent()
      await page.getByRole('button', { name: '新建证书' }).click()
      await page.getByRole('dialog').waitFor()
      await capture('certs-create')
      await closeDialog()
    })

    await step('pki', async () => {
      await open('/pki', '内部 PKI')
      await capture('pki')
    })

    await step('relays', async () => {
      await open('/relay-listeners', 'Relay 监听器')
      await capture('relays')
      await chooseLocalAgent()
      await page.getByRole('button', { name: '新建监听器' }).click()
      await page.getByRole('dialog').waitFor()
      await capture('relays-create')
      await closeDialog()
    })

    await step('versions', async () => {
      await open('/versions', '版本策略')
      await capture('versions')
    })

    await step('plugins', async () => {
      await open('/plugins', '已安装插件')
      await capture('plugins')
      await open('/plugins/official.emby-helper', 'Emby 助手')
      await capture('plugin-detail')
      await open('/plugins/marketplace', '插件市场')
      await capture('marketplace')
      await page.getByRole('button', { name: '列表视图' }).click()
      await capture('marketplace-list')
      await page.getByRole('button', { name: '卡片视图' }).click()
      await page.locator('[data-test="marketplace-package-community.ddns"]').click()
      await capture('marketplace-inspect')
      await page.locator('[data-test="marketplace-inspect-action"]').click()
      await page.locator('[data-test="marketplace-confirm-submit"]').waitFor()
      await capture('marketplace-install')
      let pendingInstall = false
      let installedDDNS = false
      const ddns = { plugin_id: 'community.ddns', name: '动态域名', active_version: '0.9.1' }
      await page.route('**/panel-api/plugins', (route) => json(route, {
        plugins: [...plugins, ...(pendingInstall ? [{ ...ddns, pending_operation_id: 'op-configure', pending_kind: 'configure' }] : installedDDNS ? [ddns] : [])]
      }))
      await page.route('**/panel-api/plugins/install', (route) => json(route, { message: '暂时连不上服务，请稍后重试。' }, 503))
      await page.locator('[data-test="marketplace-confirm-submit"]').click()
      await page.locator('[data-test="marketplace-action-error"]').waitFor()
      await capture('marketplace-install-error')
      await page.unroute('**/panel-api/plugins/install')
      await page.route('**/panel-api/plugins/install', (route) => {
        pendingInstall = true
        return json(route, { message: 'plugin state conflict', details: 'another plugin operation is already pending' }, 409)
      })
      await page.locator('[data-test="marketplace-confirm-submit"]').click()
      await page.locator('[data-test="marketplace-pending-status"]').waitFor()
      await capture('marketplace-pending')
      await page.route('**/panel-api/plugins/community.ddns', (route) => {
        const detail = pluginDetail(ddns.plugin_id)
        detail.plugin = { ...detail.plugin, ...ddns, pending_operation_id: 'op-configure', pending_kind: 'configure' }
        detail.package.manifest.name = ddns.name
        return json(route, detail)
      })
      await page.route('**/panel-api/plugins/community.ddns/operations', (route) => json(route, {
        operations: [{ id: 'op-configure', kind: 'configure', status: 'running', created_at: '2026-10-01T08:00:00Z', agent_results: { local: { state: 'running' } } }]
      }))
      await page.locator('[data-test="marketplace-pending-detail"]').click()
      await page.locator('[data-test="plugin-pending-progress"]').waitFor()
      await capture('plugin-operation-progress')
      await open('/plugins/marketplace', '插件市场')
      await page.locator('[data-test="marketplace-card-action-community.ddns"]').click()
      await page.locator('[data-test="marketplace-pending-status"]').waitFor()
      pendingInstall = false
      installedDDNS = true
      await page.locator('[data-test="marketplace-pending-refresh"]').click()
      await page.locator('[data-test="marketplace-pending-status"]').waitFor({ state: 'hidden' })
      await capture('marketplace-operation-complete')
      await closeDialog()
      await page.unroute('**/panel-api/plugins')
      await page.unroute('**/panel-api/plugins/install')
      await page.unroute('**/panel-api/plugins/community.ddns')
      await page.unroute('**/panel-api/plugins/community.ddns/operations')
      await open('/plugins/marketplace/official.waf?source=official', '网站防火墙')
      await capture('marketplace-detail')
      await open('/plugins/repositories', '插件仓库')
      await capture('repositories')
    })

    await step('settings', async () => {
      await open('/settings', '系统设置')
      await capture('settings')
      for (const tab of ['备份恢复', '网络出口', '系统关于']) {
        await page.getByRole('button', { name: tab }).click()
        await capture(`settings-${tab}`)
      }
    })

    await context.close()
  }
}

await browser.close()
fs.writeFileSync(path.join(outDir, 'report.json'), JSON.stringify({ baseURL, themes, widths, failures }, null, 2))
console.log(JSON.stringify({ failures }, null, 2))
if (failures.length) process.exitCode = 1
