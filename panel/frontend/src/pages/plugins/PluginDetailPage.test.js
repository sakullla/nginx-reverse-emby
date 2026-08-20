import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDetailPage from './PluginDetailPage.vue'

const mocks = vi.hoisted(() => ({
  fetchPluginDetail: vi.fn(), fetchPluginOperations: vi.fn(), configurePlugin: vi.fn(), publishPlugin: vi.fn(),
  enablePlugin: vi.fn(), disablePlugin: vi.fn(), rollbackPlugin: vi.fn(), uninstallPlugin: vi.fn(), deletePluginInstance: vi.fn(),
  invokePluginDynamicAction: vi.fn(), fetchPluginLogs: vi.fn(), fetchAgents: vi.fn(), fetchHttpRulesPage: vi.fn(),
  fetchAllAgentsRules: vi.fn(), fetchResourceGroups: vi.fn(), retryRevision: vi.fn(), push: vi.fn(), refreshActor: vi.fn(),
  actor: { permissions: ['*'], visible_resource_groups: [] }
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'official.waf' } }), useRouter: () => ({ push: mocks.push }) }))
vi.mock('../../api', () => ({
  fetchAgents: mocks.fetchAgents,
  fetchHttpRulesPage: mocks.fetchHttpRulesPage,
  fetchAllAgentsRules: mocks.fetchAllAgentsRules
}))
vi.mock('../../api/access', () => ({ fetchResourceGroups: mocks.fetchResourceGroups }))
vi.mock('../../api/plugins', () => ({
  fetchPluginDetail: mocks.fetchPluginDetail, fetchPluginOperations: mocks.fetchPluginOperations, configurePlugin: mocks.configurePlugin,
  publishPlugin: mocks.publishPlugin,
  enablePlugin: mocks.enablePlugin, disablePlugin: mocks.disablePlugin, rollbackPlugin: mocks.rollbackPlugin, uninstallPlugin: mocks.uninstallPlugin, deletePluginInstance: mocks.deletePluginInstance,
  invokePluginDynamicAction: mocks.invokePluginDynamicAction, fetchPluginLogs: mocks.fetchPluginLogs
}))
vi.mock('../../api/operations', () => ({ retryRevision: mocks.retryRevision }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      actor: { value: mocks.actor },
      can: (permission) => (mocks.actor.permissions || []).includes('*') || (mocks.actor.permissions || []).includes(permission),
      refreshActor: mocks.refreshActor
    })
  }
})
vi.mock('../../components/DeleteConfirmDialog.vue', () => ({
  default: {
    name: 'DeleteConfirmDialog',
    props: ['show', 'title', 'message', 'name', 'confirmText', 'loading'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" class="delete-dialog-stub"><div class="delete-dialog-title">{{ title }}</div><button class="delete-dialog-confirm" @click="$emit(\'confirm\')">{{ confirmText }}</button><button class="delete-dialog-cancel" @click="$emit(\'cancel\')">取消</button></div>'
  }
}))
vi.mock('../../components/base/BaseModal.vue', () => ({
  default: {
    name: 'BaseModal',
    props: ['modelValue', 'title', 'subtitle', 'size', 'showFooter', 'closeOnClickModal', 'dataTest'],
    emits: ['update:modelValue', 'confirm'],
    template: '<div v-if="modelValue" class="base-modal-stub" :data-test="dataTest"><button type="button" class="base-modal-close" data-test="plugin-modal-cancel" @click="$emit(\'update:modelValue\', false)">取消</button><slot /><slot name="footer" /></div>'
  }
}))

function makeInstance(overrides = {}) {
  return {
    id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [],
    bindings: [{ consumer: { kind: 'http_rule', id: '1', resource_group_id: 'group-a', version: 'a'.repeat(64) }, target_agent_id: 'edge-a' }],
    config: { mode: 'observe' }, config_version: 1, current_state: 'active',
    ...overrides
  }
}

function makeDetail(overrides = {}) {
  return {
    plugin: { plugin_id: 'official.waf', current_lifecycle: 'active', state_version: 2, active_source_kind: 'official', active_source_risk_label: 'official' },
    package: { version: '1.0.0', manifest: { id: 'official.waf', name: 'WAF' }, runtime: { kind: 'wasm-policy', abi: 'nre:policy/v1' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] }, config_schema: { type: 'object', properties: { mode: { type: 'string', title: '模式' } } } },
    instances: [makeInstance()],
    published_entries: [],
    agent_statuses: [],
    ...overrides
  }
}

function withHTTPBackend(detail, providers = [{ id: 'default', display_name: 'Default' }]) {
  return {
    ...detail,
    package: {
      ...detail.package,
      manifest: { ...detail.package.manifest, http_backend_providers: providers }
    }
  }
}

function undeployedDetail(overrides = {}) {
  return makeDetail({
    plugin: { ...makeDetail().plugin, current_lifecycle: 'disabled', desired_lifecycle: 'disabled', ...(overrides.plugin || {}) },
    instances: [],
    published_entries: [],
    agent_statuses: [],
    ...overrides
  })
}

function unpublishedHTTPDetail(overrides = {}) {
  return withHTTPBackend(makeDetail({
    instances: [makeInstance({ bindings: [] })],
    published_entries: [],
    ...overrides
  }))
}

function publishedHTTPDetail(overrides = {}) {
  const entry = {
    rule_id: 12,
    agent_id: 'edge-a',
    frontend_url: 'https://media.example.com',
    enabled: true,
    accessible: true,
    ...(overrides.entry || {})
  }
  const rest = { ...overrides }
  delete rest.entry
  return withHTTPBackend(makeDetail({
    instances: [makeInstance({ bindings: [{ consumer: { kind: 'http_rule', id: String(entry.rule_id) }, target_agent_id: entry.agent_id }] })],
    published_entries: [entry],
    ...rest
  }))
}

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text() === text)
}

function pagePrimaryButtons(wrapper) {
  return wrapper.findAll('button.btn-primary').filter((button) => {
    const modal = button.element.closest('[data-test="plugin-deploy-modal"], [data-test="plugin-instance-config-modal"], .base-modal-stub, .delete-dialog-stub')
    return !modal && button.isVisible()
  })
}

function taskGuide(wrapper) {
  const modal = wrapper.find('[data-test="plugin-deploy-modal"]')
  if (modal.exists()) return modal
  const guide = wrapper.find('[data-test="plugin-task-guide"]')
  if (guide.exists()) return guide
  return wrapper
}

async function openGuide(wrapper, actionText = '开始部署') {
  const existing = taskGuide(wrapper)
  if (existing !== wrapper && (existing.attributes('data-test') === 'plugin-deploy-modal' || existing.attributes('data-test') === 'plugin-task-guide')) {
    return existing
  }
  const button = buttonByText(wrapper, actionText) || wrapper.find('[data-test="plugin-task-primary"]')
  expect(button && (button.exists?.() ?? true)).toBeTruthy()
  await button.trigger('click')
  await flushPromises()
  return wrapper.get('[data-test="plugin-deploy-modal"]')
}

function deployModal(wrapper) {
  return wrapper.find('[data-test="plugin-deploy-modal"]')
}

function configModal(wrapper) {
  return wrapper.find('[data-test="plugin-instance-config-modal"]')
}

function modalButton(modal, text) {
  return modal.findAll('button').find((button) => button.text() === text)
}

function guideSubmit(guide) {
  return ['发布到域名', '保存入口', '开始部署', '部署并发布', '发布', '部署']
    .map((label) => buttonByText(guide, label))
    .find(Boolean) || guide.find('[data-test="plugin-task-primary"]')
}

function findControl(root, testIds) {
  for (const id of testIds) {
    const node = root.find(`[data-test="${id}"]`)
    if (!node.exists()) continue
    if (['INPUT', 'SELECT', 'TEXTAREA'].includes(node.element.tagName)) return node
    const inner = node.find('input, select, textarea')
    return inner.exists() ? inner : node
  }
  return null
}

async function selectTarget(guide, agentId) {
  const named = findControl(guide, ['plugin-guide-target', 'deployment-target', 'deployment-agent', 'plugin-deploy-target'])
  if (named && named.element.tagName === 'SELECT') {
    await named.setValue(agentId)
    return
  }
  const match = guide.findAll('[data-test="plugin-guide-target"], [data-test="deployment-agent"], input[type="radio"], .plugin-deployment__agent input')
    .find((input) => input.element.value === agentId)
  if (!match) throw new Error(`deployment target ${agentId} was not rendered`)
  if (match.element.type === 'checkbox' || match.element.type === 'radio') await match.setChecked(true)
  else await match.setValue(agentId)
}

async function fillDomain(guide, { host, https = true }) {
  const hostNode = findControl(guide, ['plugin-guide-domain', 'deployment-frontend-host', 'deployment-domain', 'plugin-publish-domain'])
  const urlNode = findControl(guide, ['deployment-frontend-url', 'plugin-publish-frontend-url'])
  const httpsNode = findControl(guide, ['plugin-guide-https', 'deployment-https', 'plugin-publish-https'])
  if (hostNode) await hostNode.setValue(host)
  else if (urlNode) await urlNode.setValue(`${https ? 'https' : 'http'}://${host}`)
  else throw new Error('entry domain field was not rendered')
  if (!httpsNode) {
    if (urlNode) return
    throw new Error('HTTPS field was not rendered')
  }
  if (httpsNode.element.tagName === 'SELECT') await httpsNode.setValue(https ? 'https' : 'http')
  else if (httpsNode.element.type === 'checkbox' || httpsNode.element.type === 'radio') await httpsNode.setChecked(https)
  else await httpsNode.setValue(https)
}

function expectNoProviderOrRuleDetour(wrapper) {
  expect(wrapper.text()).not.toContain('到 HTTP 规则添加')
  expect(wrapper.text()).not.toContain('选择插件提供商')
}

async function openConfigModal(wrapper) {
  await buttonByText(wrapper, '编辑配置').trigger('click')
  return wrapper.get('[data-test="plugin-instance-config-modal"]')
}

const opsLabels = ['启用', '停用', '回滚', '卸载', '导出脱敏诊断']

function morePanel(wrapper) {
  return wrapper.find('[data-test="plugin-more"]')
}

async function openMore(wrapper) {
  const panel = wrapper.get('[data-test="plugin-more"]')
  panel.element.open = true
  await flushPromises()
  return panel
}

function firstScreenRoot(wrapper) {
  const clone = wrapper.element.cloneNode(true)
  clone.querySelectorAll('[data-test="plugin-more"], [data-test="plugin-deploy-modal"], [data-test="plugin-instance-config-modal"], .base-modal-stub, .delete-dialog-stub').forEach((node) => node.remove())
  return clone
}

function firstScreenText(wrapper) {
  return firstScreenRoot(wrapper).textContent || ''
}

function firstScreenButtons(wrapper) {
  return wrapper.findAll('button').filter((button) => {
    const hidden = button.element.closest('[data-test="plugin-more"], [data-test="plugin-deploy-modal"], [data-test="plugin-instance-config-modal"], .base-modal-stub, .delete-dialog-stub')
    return !hidden && button.isVisible()
  })
}

function expectOpsOnlyInMore(wrapper, { allowUninstall = false } = {}) {
  const screen = firstScreenText(wrapper)
  const hidden = allowUninstall ? opsLabels.filter((label) => label !== '卸载') : opsLabels
  for (const label of hidden) {
    expect(firstScreenButtons(wrapper).some((button) => button.text() === label)).toBe(false)
  }
  expect(screen).not.toContain('导出脱敏诊断')
  expect(screen).not.toMatch(/逐 Agent 状态/)
  expect(screen).not.toMatch(/运行日志/)
  expect(screen).not.toMatch(/生命周期操作与审计|操作时间线/)
  const more = morePanel(wrapper)
  expect(more.exists()).toBe(true)
  expect(more.get('summary').text()).toBe('更多')
  expect(more.element.open).toBeFalsy()
}

beforeEach(() => {
  mocks.fetchPluginDetail.mockReset().mockResolvedValue(makeDetail())
  mocks.fetchPluginOperations.mockReset().mockResolvedValue([{ id: 'op', kind: 'configure', status: 'failed', error: 'token=raw-token', agent_results: {} }])
  mocks.configurePlugin.mockReset().mockResolvedValue({})
  mocks.publishPlugin.mockReset().mockResolvedValue({ instance: { id: 'official.waf-default' }, published_entries: [] })
  mocks.enablePlugin.mockReset().mockResolvedValue({})
  mocks.disablePlugin.mockReset().mockResolvedValue({})
  mocks.rollbackPlugin.mockReset().mockResolvedValue({})
  mocks.uninstallPlugin.mockReset().mockResolvedValue({})
  mocks.deletePluginInstance.mockReset().mockResolvedValue(true)
  mocks.invokePluginDynamicAction.mockReset()
  mocks.fetchPluginLogs.mockReset().mockResolvedValue({ entries: [], next_cursor: '' })
  mocks.fetchAgents.mockReset().mockResolvedValue([
    { id: 'edge-a', name: 'Edge A', status: 'online', desired_revision: 1, current_revision: 1, last_apply_status: 'success' },
    { id: 'edge-b', name: 'Edge B', status: 'online', desired_revision: 2, current_revision: 2, last_apply_status: 'success' }
  ])
  mocks.fetchResourceGroups.mockReset().mockResolvedValue([
    { id: 'default', name: '默认组' },
    { id: 'team', name: '团队组' }
  ])
  mocks.fetchHttpRulesPage.mockReset().mockResolvedValue({ items: [], total: 0 })
  mocks.fetchAllAgentsRules.mockReset().mockResolvedValue([])
  mocks.retryRevision.mockReset().mockResolvedValue({})
  mocks.push.mockReset()
  mocks.refreshActor.mockReset()
  mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
})

async function mountPage(detail = makeDetail()) {
  mocks.fetchPluginDetail.mockResolvedValue(detail)
  const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}

describe('PluginDetailPage', () => {
  it('refreshes plugin state in the background and stops after unmount', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = await mountPage()
      expect(mocks.fetchPluginDetail).toHaveBeenCalledTimes(1)

      await vi.advanceTimersByTimeAsync(5000)
      await flushPromises()
      expect(mocks.fetchPluginDetail).toHaveBeenCalledTimes(2)
      expect(wrapper.find('.plugin-detail-page__loading').exists()).toBe(false)

      wrapper.unmount()
      await vi.advanceTimersByTimeAsync(5000)
      expect(mocks.fetchPluginDetail).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('retries the newest target revision when the Agent projection is stale', async () => {
    const detail = makeDetail({ agent_statuses: [{
      instance_id: 'waf-a', agent_id: 'edge-a', target_scope: 'active', runtime_state: 'failed',
      desired_revision: 1, target_revision: 2, current_revision: 1, operation_kind: 'configure', operation_status: 'failed'
    }] })
    const wrapper = await mountPage(detail)
    expect(firstScreenButtons(wrapper).some((button) => button.text() === '重试此 Agent revision')).toBe(false)
    const more = await openMore(wrapper)
    await buttonByText(more, '重试此 Agent revision').trigger('click')
    await flushPromises()
    expect(mocks.retryRevision).toHaveBeenCalledWith(
      expect.objectContaining({ agent_id: 'edge-a', desired_revision: 2 }),
      expect.objectContaining({ agent_id: 'edge-a', desired_revision: 2 })
    )
  })

  it('shows start-deploy as the first-screen primary action when the plugin is not deployed', async () => {
    const wrapper = await mountPage(undeployedDetail())
    expect(wrapper.find('.page-header').exists()).toBe(true)
    expect(wrapper.find('.page-title').text()).toBe('WAF')
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toBe('还没部署')
    expect(wrapper.get('.plugin-task__purpose').text()).toContain('把插件部署到一个节点后即可在该节点上使用')
    expect(pagePrimaryButtons(wrapper).map((button) => button.text())).toEqual(['开始部署'])
    expect(wrapper.find('[data-test="plugin-task-uninstall"]').exists()).toBe(true)
    expect(firstScreenText(wrapper)).not.toContain('模式')
    expect(wrapper.find('[data-test="plugin-task-center"] .declarative-field').exists()).toBe(false)
    expectOpsOnlyInMore(wrapper, { allowUninstall: true })
    const more = await openMore(wrapper)
    expect(buttonByText(more, '启用')?.classes() || []).not.toContain('btn-primary')
    expect(buttonByText(more, '回滚')?.classes() || []).not.toContain('btn-primary')
    expect(buttonByText(more, '导出脱敏诊断')?.classes() || []).not.toContain('btn-primary')
    expect(buttonByText(more, '卸载').classes()).toContain('btn-danger')
  })

  it('switches instances with BaseTabs instead of a native select', async () => {
    const detail = makeDetail()
    detail.instances = [
      makeInstance({ id: 'waf-a', resource_group_id: 'group-a', bindings: [], config: { mode: 'observe' } }),
      makeInstance({ id: 'waf-b', resource_group_id: 'group-b', targets: ['edge-b'], bindings: [], config: { mode: 'block' }, config_version: 2 })
    ]
    const wrapper = await mountPage(detail)
    const tablist = wrapper.find('[role="tablist"]')
    expect(tablist.exists()).toBe(true)
    expect(tablist.text()).toContain('waf-a · group-a')
    expect(tablist.text()).toContain('waf-b · group-b')
    expect(wrapper.find('select option[value="waf-a"]').exists()).toBe(false)
  })

  it('submits host-schema config with caller-owned binding fields and redacts errors', async () => {
    const wrapper = await mountPage()
    expect(wrapper.text()).not.toContain('raw-token')
    expect(configModal(wrapper).exists()).toBe(false)
    expect(wrapper.find('[data-test="plugin-task-center"] .declarative-field').exists()).toBe(false)
    expect(wrapper.find('.instance-facts .declarative-field').exists()).toBe(false)
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', {
      instance_id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [],
      bindings: [{ consumer: { kind: 'http_rule', id: '1' }, target_agent_id: 'edge-a' }], config: { mode: 'block' }, secret_replacements: {}
    })
  })

  it('omits projected http_rule bindings when saving config after HTTP publish', async () => {
    const wrapper = await mountPage(publishedHTTPDetail())
    const modal = await openConfigModal(wrapper)
    expect(modal.find('[data-test="plugin-publish-needed"]').exists()).toBe(false)
    expect(modal.text()).not.toContain('还差发布')
    expect(modal.get('[data-test="plugin-published-entry"]').text()).toContain('https://media.example.com')
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', {
      instance_id: 'waf-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [],
      bindings: [], config: { mode: 'block' }, secret_replacements: {}
    })
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
  })

  it('deploys a non-HTTP plugin to exactly one selected node', async () => {
    mocks.configurePlugin.mockResolvedValue({ id: 'official.waf-default' })
    const wrapper = await mountPage(undeployedDetail())
    expect(deployModal(wrapper).exists()).toBe(false)
    const guide = await openGuide(wrapper)

    expect(findControl(guide, ['plugin-guide-domain', 'deployment-frontend-host', 'deployment-domain'])).toBeNull()
    expect(findControl(guide, ['plugin-guide-https', 'deployment-https'])).toBeNull()
    expect(guide.text()).not.toContain('选择全部')
    expect(guide.findAll('.plugin-deployment__agent input[type="checkbox"]').length).toBe(0)
    const groupSelect = findControl(guide, ['plugin-guide-resource-group', 'deployment-resource-group'])
    expect(groupSelect).toBeTruthy()
    expect(groupSelect.element.tagName).toBe('SELECT')
    expect(groupSelect.element.value).toBe('default')
    await selectTarget(guide, 'edge-a')
    await guide.get('.declarative-field input[type="text"]').setValue('block')
    await guideSubmit(guide).trigger('click')
    await flushPromises()

    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', {
      instance_id: 'official.waf-default',
      resource_group_id: 'default',
      targets: ['edge-a'],
      policy_chains: [],
      bindings: [],
      config: { mode: 'block' },
      secret_replacements: {}
    })
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
    expect(mocks.enablePlugin).toHaveBeenCalledWith('official.waf')
    expect(wrapper.text()).not.toContain('还没发布域名')
  })

  it('publishes an HTTP-backend plugin with one node, one domain, and HTTPS from the same guide', async () => {
    mocks.publishPlugin.mockResolvedValue({
      instance: { id: 'official.waf-default' },
      published_entries: [{ rule_id: 12, agent_id: 'edge-a', frontend_url: 'https://media.example.com', enabled: true, accessible: true }]
    })
    const wrapper = await mountPage(withHTTPBackend(undeployedDetail()))
    const guide = await openGuide(wrapper)
    expect(guide.text()).not.toContain('选择全部')
    expect(guide.findAll('.plugin-deployment__agent input[type="checkbox"]').length).toBe(0)
    expectNoProviderOrRuleDetour(guide)
    await selectTarget(guide, 'edge-a')
    await fillDomain(guide, { host: 'media.example.com', https: true })
    await guide.get('.declarative-field input[type="text"]').setValue('block')
    await guideSubmit(guide).trigger('click')
    await flushPromises()

    expect(mocks.publishPlugin).toHaveBeenCalledTimes(1)
    expect(mocks.publishPlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      instance_id: 'official.waf-default',
      resource_group_id: 'default',
      targets: ['edge-a'],
      policy_chains: [],
      frontend_url: 'https://media.example.com',
      config: { mode: 'block' }
    }))
    expect(mocks.publishPlugin.mock.calls[0][1]).not.toHaveProperty('provider_id')
    expect(mocks.publishPlugin.mock.calls[0][1]).not.toHaveProperty('rule_id')
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    expect(mocks.enablePlugin).not.toHaveBeenCalled()
  })

  it('keeps the HTTP publish submit disabled until a node and domain are present', async () => {
    const wrapper = await mountPage(withHTTPBackend(undeployedDetail()))
    const guide = await openGuide(wrapper)
    await guide.get('.declarative-field input[type="text"]').setValue('block')
    expect(guideSubmit(guide).attributes('disabled')).toBeDefined()
    await selectTarget(guide, 'edge-a')
    expect(guideSubmit(guide).attributes('disabled')).toBeDefined()
    await fillDomain(guide, { host: 'media.example.com', https: false })
    expect(guideSubmit(guide).attributes('disabled')).toBeUndefined()
    await guideSubmit(guide).trigger('click')
    await flushPromises()
    expect(mocks.publishPlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      targets: ['edge-a'],
      frontend_url: 'http://media.example.com'
    }))
  })

  it('prompts for a domain when an HTTP-backend plugin is deployed but unpublished', async () => {
    const wrapper = await mountPage(unpublishedHTTPDetail())
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toMatch(/还差发布|还没发布域名/)
    expect(wrapper.text()).toContain('还差发布')
    expect(wrapper.text()).not.toContain('已可用')
    expect(pagePrimaryButtons(wrapper).some((button) => button.text() === '发布到域名')).toBe(true)
    const guide = await openGuide(wrapper, '发布到域名')
    expect(findControl(guide, ['plugin-guide-domain', 'deployment-frontend-host', 'deployment-domain'])).toBeTruthy()
    expect(findControl(guide, ['plugin-guide-https', 'deployment-https'])).toBeTruthy()
    expectNoProviderOrRuleDetour(wrapper)
  })

  it('does not treat a deployed non-HTTP plugin as waiting to publish a domain', async () => {
    const wrapper = await mountPage(makeDetail({ instances: [makeInstance({ bindings: [] })], published_entries: [] }))
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toBe('已部署')
    expect(wrapper.text()).not.toMatch(/还差发布|还没发布域名/)
    expect(buttonByText(wrapper, '发布到域名')).toBeUndefined()
    expect(wrapper.find('[data-test="plugin-guide-domain"]').exists()).toBe(false)
  })

  it('still shows undeployed or unpublished after leaving the guide without submitting', async () => {
    const undeployed = await mountPage(withHTTPBackend(undeployedDetail()))
    const deployGuide = await openGuide(undeployed)
    await selectTarget(deployGuide, 'edge-a')
    await fillDomain(deployGuide, { host: 'draft.example.com' })
    undeployed.unmount()

    const againUndeployed = await mountPage(withHTTPBackend(undeployedDetail()))
    expect(againUndeployed.get('[data-test="plugin-task-status"]').text()).toBe('还没部署')
    expect(againUndeployed.text()).not.toContain('已可用')
    expect(buttonByText(againUndeployed, '开始部署')).toBeTruthy()

    const unpublished = await mountPage(unpublishedHTTPDetail())
    const publishGuide = await openGuide(unpublished, '发布到域名')
    await fillDomain(publishGuide, { host: 'draft.example.com' })
    unpublished.unmount()

    const againUnpublished = await mountPage(unpublishedHTTPDetail())
    expect(againUnpublished.get('[data-test="plugin-task-status"]').text()).toMatch(/还差发布|还没发布域名/)
    expect(againUnpublished.text()).not.toContain('已可用')
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
  })

  it('shows the published entry and updates the original rule from the detail page', async () => {
    const wrapper = await mountPage(publishedHTTPDetail())
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toBe('已可用')
    const entries = wrapper.get('[data-test="plugin-published-entries"]')
    expect(entries.text()).toContain('HTTP 入口')
    expect(entries.get('[data-test="plugin-published-entry"]').text()).toContain('https://media.example.com')
    expect(entries.get('[data-test="plugin-published-entry"]').text()).toContain('已启用')
    expect(entries.get('[data-test="plugin-published-entry"]').text()).toContain('可访问')
    expect(entries.find('a.plugin-http-entry__url').attributes('href')).toBe('https://media.example.com')
    expectNoProviderOrRuleDetour(wrapper)

    await buttonByText(wrapper, '修改入口').trigger('click')
    await flushPromises()
    const guide = taskGuide(wrapper)
    await fillDomain(guide, { host: 'media-v2.example.com', https: true })
    await guideSubmit(guide).trigger('click')
    await flushPromises()
    expect(mocks.publishPlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      rule_id: 12,
      targets: ['edge-a'],
      frontend_url: 'https://media-v2.example.com'
    }))
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
  })

  it('publishes another domain as a separate entry without requiring the rules page', async () => {
    const wrapper = await mountPage(publishedHTTPDetail())
    await buttonByText(wrapper, '再发布一条域名').trigger('click')
    await flushPromises()
    const guide = taskGuide(wrapper)
    await selectTarget(guide, 'edge-b')
    await fillDomain(guide, { host: 'alt.example.com', https: true })
    await guideSubmit(guide).trigger('click')
    await flushPromises()
    expect(mocks.publishPlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      targets: ['edge-b'],
      frontend_url: 'https://alt.example.com'
    }))
    expect(mocks.publishPlugin.mock.calls[0][1]).not.toHaveProperty('rule_id')
    expectNoProviderOrRuleDetour(guide)
  })

  it('marks a published entry that is not yet reachable instead of calling it available', async () => {
    const wrapper = await mountPage(publishedHTTPDetail({
      entry: { accessible: false, enabled: true, frontend_url: 'https://media.example.com' }
    }))
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toBe('已发布但还不能访问')
    expect(wrapper.get('[data-test="plugin-published-entry"]').text()).toContain('https://media.example.com')
    expect(wrapper.get('[data-test="plugin-published-entry"]').text()).toContain('还不能访问')
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).not.toContain('已可用')
  })

  it('confirms uninstall through DeleteConfirmDialog and navigates away', async () => {
    const detail = makeDetail()
    detail.plugin.current_lifecycle = 'disabled'
    const wrapper = await mountPage(detail)
    expectOpsOnlyInMore(wrapper)
    const more = await openMore(wrapper)
    await buttonByText(more, '卸载').trigger('click')
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.disablePlugin).not.toHaveBeenCalled()
    expect(mocks.uninstallPlugin).toHaveBeenCalledWith('official.waf')
    expect(mocks.push).toHaveBeenCalledWith('/plugins')
  })

  it('uninstalls an undeployed applying plugin from the task card without disabling first', async () => {
    const wrapper = await mountPage(makeDetail({
      plugin: { ...makeDetail().plugin, current_lifecycle: 'applying', desired_lifecycle: 'enabled' },
      instances: [],
      published_entries: [],
      agent_statuses: []
    }))
    const uninstall = wrapper.get('[data-test="plugin-task-uninstall"]')
    expect(uninstall.attributes('disabled')).toBeUndefined()
    await uninstall.trigger('click')
    expect(wrapper.find('.delete-dialog-title').text()).toBe('确认卸载插件')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.disablePlugin).not.toHaveBeenCalled()
    expect(mocks.uninstallPlugin).toHaveBeenCalledWith('official.waf')
    expect(mocks.push).toHaveBeenCalledWith('/plugins')
  })

  it('disables an active plugin before uninstalling it', async () => {
    const wrapper = await mountPage(makeDetail())
    const more = await openMore(wrapper)
    const uninstall = buttonByText(more, '卸载')
    expect(uninstall.attributes('disabled')).toBeUndefined()
    await uninstall.trigger('click')
    expect(wrapper.find('.delete-dialog-title').text()).toBe('确认卸载插件')
    expect(wrapper.text()).toContain('会先停用')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.disablePlugin).toHaveBeenCalledWith('official.waf')
    expect(mocks.uninstallPlugin).toHaveBeenCalledWith('official.waf')
    expect(mocks.push).toHaveBeenCalledWith('/plugins')
  })

  it('keeps uninstall available after a pending-operation conflict and humanizes the error', async () => {
    mocks.uninstallPlugin.mockRejectedValue(new Error('plugin state conflict: another plugin operation is already pending'))
    const detail = makeDetail()
    detail.plugin.current_lifecycle = 'disabled'
    detail.plugin.pending_operation_id = 'op-upgrade'
    const wrapper = await mountPage(detail)
    const more = await openMore(wrapper)
    await buttonByText(more, '卸载').trigger('click')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(wrapper.find('.plugin-alert').text()).toContain('未完成的操作')
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('confirms deletion of only the selected deployment instance', async () => {
    const wrapper = await mountPage(makeDetail({
      instances: [
        makeInstance({ bindings: [] }),
        makeInstance({ id: 'waf-b', targets: ['edge-b'], bindings: [], config: { mode: 'block' } })
      ]
    }))
    await buttonByText(wrapper, '删除实例').trigger('click')
    expect(wrapper.find('.delete-dialog-title').text()).toBe('确认删除部署实例')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.deletePluginInstance).toHaveBeenCalledWith('official.waf', 'waf-a')
    expect(mocks.uninstallPlugin).not.toHaveBeenCalled()
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('enables immediately but gates disable behind DeleteConfirmDialog', async () => {
    const wrapper = await mountPage()
    const more = await openMore(wrapper)

    await buttonByText(more, '启用').trigger('click')
    await flushPromises()
    expect(mocks.enablePlugin).toHaveBeenCalledWith('official.waf')

    const moreAfterEnable = await openMore(wrapper)
    await buttonByText(moreAfterEnable, '停用').trigger('click')
    expect(mocks.disablePlugin).not.toHaveBeenCalled()
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)
    expect(wrapper.find('.delete-dialog-title').text()).toBe('确认停用插件')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.disablePlugin).toHaveBeenCalledWith('official.waf')
  })

  it('cancels dangerous lifecycle actions without writing', async () => {
    const detail = makeDetail()
    detail.plugin.rollback_package_digest = 'sha256:rollback-digest'
    const wrapper = await mountPage(detail)

    const more = await openMore(wrapper)
    await buttonByText(more, '停用').trigger('click')
    await wrapper.find('.delete-dialog-cancel').trigger('click')
    expect(mocks.disablePlugin).not.toHaveBeenCalled()
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(false)

    await buttonByText(more, '回滚').trigger('click')
    await wrapper.find('.delete-dialog-cancel').trigger('click')
    expect(mocks.rollbackPlugin).not.toHaveBeenCalled()

    await buttonByText(wrapper, '删除实例').trigger('click')
    await wrapper.find('.delete-dialog-cancel').trigger('click')
    expect(mocks.deletePluginInstance).not.toHaveBeenCalled()
  })

  it('requires confirmation before rolling back', async () => {
    const detail = makeDetail()
    detail.plugin.rollback_package_digest = 'sha256:rollback-digest'
    const wrapper = await mountPage(detail)

    const more = await openMore(wrapper)
    await buttonByText(more, '回滚').trigger('click')
    expect(mocks.rollbackPlugin).not.toHaveBeenCalled()
    expect(wrapper.find('.delete-dialog-title').text()).toBe('确认回滚插件')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(mocks.rollbackPlugin).toHaveBeenCalledWith('official.waf', [])
  })

  it('keeps enable, diagnostics, logs, timeline, and technical details only in 更多', async () => {
    const wrapper = await mountPage(undeployedDetail())
    expect(pagePrimaryButtons(wrapper).map((button) => button.text())).toEqual(['开始部署'])
    expectOpsOnlyInMore(wrapper, { allowUninstall: true })
    const more = await openMore(wrapper)
    for (const label of opsLabels) {
      expect(buttonByText(more, label)).toBeTruthy()
    }
    expect(more.find('.plugin-technical').exists()).toBe(true)
    expect(more.text()).toMatch(/逐 Agent 状态/)
    expect(more.text()).toMatch(/运行日志|操作时间线|生命周期/)
  })

  it('submits the selected visible resource group and does not require an instance id field', async () => {
    mocks.configurePlugin.mockResolvedValue({ id: 'official.waf-default' })
    const wrapper = await mountPage(undeployedDetail())
    const guide = await openGuide(wrapper)
    await findControl(guide, ['plugin-guide-resource-group', 'deployment-resource-group']).setValue('team')
    await selectTarget(guide, 'edge-a')
    await guide.get('.declarative-field input[type="text"]').setValue('block')
    await guideSubmit(guide).trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      instance_id: 'official.waf-default',
      resource_group_id: 'team',
      targets: ['edge-a'],
      policy_chains: [],
      bindings: []
    }))
  })

  it('keeps an installed plugin with no visible instances and opens the deploy guide on demand', async () => {
    const wrapper = await mountPage(undeployedDetail({ instances: [], agent_statuses: [] }))
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toBe('还没部署')
    expect(deployModal(wrapper).exists()).toBe(false)
    expectOpsOnlyInMore(wrapper, { allowUninstall: true })
    expect(morePanel(wrapper).find('.plugin-technical').exists()).toBe(true)
    const modal = await openGuide(wrapper)
    expect(modal.exists()).toBe(true)
    expect(modal.attributes('data-test')).toBe('plugin-deploy-modal')
  })

  it('shows only instances from groups the current actor can see', async () => {
    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['group-a'] }
    const detail = makeDetail()
    detail.instances = [
      makeInstance({ bindings: [], config: { mode: 'observe' } }),
      makeInstance({ id: 'waf-b', resource_group_id: 'group-b', targets: ['edge-b'], bindings: [], config: { mode: 'block' }, config_version: 2 })
    ]
    const wrapper = await mountPage(detail)
    expect(wrapper.text()).toContain('waf-a · group-a')
    expect(wrapper.text()).not.toContain('waf-b · group-b')
    expect(wrapper.find('input[data-test="deployment-resource-group"]').exists()).toBe(false)
  })

  it('blocks deploy when no visible resource group or agent is selected', async () => {
    mocks.fetchResourceGroups.mockResolvedValue([])
    mocks.fetchAgents.mockResolvedValue([])
    const wrapper = await mountPage(undeployedDetail())
    const guide = await openGuide(wrapper)
    expect(guide.text()).toContain('当前身份没有可见的资源组，无法部署。')
    expect(guideSubmit(guide).attributes('disabled')).toBeDefined()
    await guideSubmit(guide).trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('当前身份没有可见的资源组，无法部署。')
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
  })

  it('does not let a resource writer submit publish or reopen the published entry form', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['group-a'] }
    const unpublished = await mountPage(unpublishedHTTPDetail())
    expect(unpublished.get('[data-test="plugin-task-status"]').text()).toMatch(/还差发布|还没发布域名/)
    expect(unpublished.text()).toContain('当前身份可以看懂下一步，但不能提交部署或发布')
    const publish = buttonByText(unpublished, '发布到域名')
    expect(publish.attributes('disabled')).toBeDefined()
    await publish.trigger('click')
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
    unpublished.unmount()

    const published = await mountPage(publishedHTTPDetail())
    expect(published.get('[data-test="plugin-published-entry"]').text()).toContain('https://media.example.com')
    expect(buttonByText(published, '修改入口')).toBeUndefined()
    expect(buttonByText(published, '再发布一条域名')).toBeUndefined()
    const modal = await openConfigModal(published)
    expect(modal.find('[data-test="plugin-publish-needed"]').exists()).toBe(false)
    expect(modal.find('[data-test="plugin-publish-submit"]').exists()).toBe(false)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalled()
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
  })

  it('lets a resource writer save a schema fallback form without declarative UI', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['group-a'] }
    const wrapper = await mountPage()
    expect(wrapper.text()).not.toContain('当前身份只有只读权限')
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      instance_id: 'waf-a',
      config: { mode: 'block' }
    }))
  })

  it('lets a resource writer save a plugin that already has declarative UI', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['group-a'] }
    const detail = makeDetail({
      package: {
        ...makeDetail().package,
        declarative_ui: {
          schema_version: 1,
          title: 'WAF',
          components: [{ type: 'text', id: 'mode', label: '模式', binding: '/mode' }],
          actions: [{ type: 'submit', id: 'save', label: '保存配置' }]
        }
      }
    })
    const wrapper = await mountPage(detail)
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      config: { mode: 'block' }
    }))
  })

  it('keeps configuration closed for members without write permission', async () => {
    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['group-a'] }
    const wrapper = await mountPage()
    expect(wrapper.text()).toContain('当前身份只有只读权限')
    expect(buttonByText(wrapper, '编辑配置')).toBeUndefined()
    expect(configModal(wrapper).exists()).toBe(false)
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
  })

  it('lets a readonly actor see the next task without submitting deploy or publish', async () => {
    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['group-a'] }
    const undeployed = await mountPage(withHTTPBackend(undeployedDetail()))
    expect(undeployed.get('[data-test="plugin-task-status"]').text()).toBe('还没部署')
    expect(undeployed.text()).toContain('当前身份可以看懂下一步，但不能提交部署或发布')
    const start = buttonByText(undeployed, '开始部署')
    if (start) {
      expect(start.attributes('disabled')).toBeDefined()
      await start.trigger('click')
    }
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    expect(mocks.publishPlugin).not.toHaveBeenCalled()

    const unpublished = await mountPage(unpublishedHTTPDetail())
    expect(unpublished.get('[data-test="plugin-task-status"]').text()).toMatch(/还差发布|还没发布域名/)
    expect(unpublished.text()).toContain('当前身份可以看懂下一步，但不能提交部署或发布')
    const publish = buttonByText(unpublished, '发布到域名')
    if (publish) {
      expect(publish.attributes('disabled')).toBeDefined()
      await publish.trigger('click')
    }
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
  })

  it('does not call configurePlugin when visible schema validation fails', async () => {
    const detail = makeDetail({
      package: {
        ...makeDetail().package,
        config_schema: {
          type: 'object',
          required: ['mode', 'port'],
          properties: {
            mode: { type: 'string', title: '模式', minLength: 2, pattern: '^[a-z]+$' },
            port: { type: 'number', title: '端口', minimum: 1, maximum: 65535 },
            sources: {
              type: 'array',
              items: {
                type: 'object',
                required: ['host'],
                properties: { host: { type: 'string', title: '主机', minLength: 2 } }
              }
            }
          }
        }
      },
      instances: [makeInstance({
        config: { mode: 'X', port: 0, sources: [{ host: 'a' }] }
      })]
    })
    const wrapper = await mountPage(detail)
    const modal = await openConfigModal(wrapper)
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    expect(modal.text()).toMatch(/至少 2 个字符|格式不匹配/)
    expect(modal.text()).toContain('不能小于 1')
  })

  it('saves nested objects and arrays from the schema fallback form', async () => {
    const detail = makeDetail({
      package: {
        ...makeDetail().package,
        config_schema: {
          type: 'object',
          properties: {
            credentials: { type: 'object', title: '凭据', properties: { region: { type: 'string', title: '区域' } } },
            sources: {
              type: 'array',
              title: '源',
              items: { type: 'object', properties: { host: { type: 'string', title: '主机' } } }
            }
          }
        }
      },
      instances: [makeInstance({
        config: { credentials: { region: 'us' }, sources: [{ host: 'a.example' }] }
      })]
    })
    const wrapper = await mountPage(detail)
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-section input[type="text"]').setValue('eu')
    await modal.findAll('button').find((button) => button.text() === '+ 添加').trigger('click')
    const itemInputs = modal.findAll('.declarative-array-item input[type="text"]')
    await itemInputs[1].setValue('b.example')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      config: { credentials: { region: 'eu' }, sources: [{ host: 'a.example' }, { host: 'b.example' }] }
    }))
  })

  it('saves an empty required array when the schema does not declare minItems', async () => {
    const detail = makeDetail({
      package: {
        ...makeDetail().package,
        config_schema: {
          type: 'object',
          required: ['apps'],
          properties: {
            apps: {
              type: 'array',
              maxItems: 128,
              items: {
                type: 'object',
                required: ['image', 'rule_ref'],
                properties: {
                  image: { type: 'string', minLength: 1 },
                  rule_ref: { type: 'string', minLength: 1 }
                }
              }
            }
          }
        }
      },
      instances: [makeInstance({ config: {} })]
    })
    const wrapper = await mountPage(detail)
    const modal = await openConfigModal(wrapper)
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(modal.text()).not.toContain('此项为必填')
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      config: { apps: [] }
    }))
  })

  it('opens the undeployed primary action into the deploy modal instead of an inline config form', async () => {
    const wrapper = await mountPage(undeployedDetail())
    expect(deployModal(wrapper).exists()).toBe(false)
    expect(wrapper.find('[data-test="plugin-task-guide"]').exists()).toBe(false)
    expect(wrapper.find('.declarative-field').exists()).toBe(false)
    expect(firstScreenText(wrapper)).toContain('把插件部署到一个节点后即可在该节点上使用')
    expect(firstScreenText(wrapper)).toContain('还没部署')

    const guide = await openGuide(wrapper)
    expect(guide.attributes('data-test')).toBe('plugin-deploy-modal')
    expect(guide.find('.declarative-field input[type="text"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="plugin-task-center"] .declarative-field').exists()).toBe(false)
  })

  it('does not write when the instance config modal is cancelled after editing', async () => {
    const wrapper = await mountPage()
    expect(wrapper.find('.instance-facts .declarative-field').exists()).toBe(false)
    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modal.get('[data-test="plugin-modal-cancel"]').trigger('click')
    await flushPromises()
    expect(configModal(wrapper).exists()).toBe(false)
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
  })

  it('does not write when the deploy modal is cancelled', async () => {
    const wrapper = await mountPage(undeployedDetail())
    const guide = await openGuide(wrapper)
    await selectTarget(guide, 'edge-a')
    await guide.get('.declarative-field input[type="text"]').setValue('block')
    await guide.get('[data-test="plugin-modal-cancel"]').trigger('click')
    await flushPromises()
    expect(deployModal(wrapper).exists()).toBe(false)
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    expect(mocks.publishPlugin).not.toHaveBeenCalled()
    expect(mocks.enablePlugin).not.toHaveBeenCalled()
  })

  it('limits deploy pickers to visible resource groups and agents', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['team'] }
    mocks.fetchResourceGroups.mockResolvedValue([
      { id: 'default', name: '默认组' },
      { id: 'team', name: '团队组' },
      { id: 'hidden', name: '隐藏组' }
    ])
    const writerPage = await mountPage(undeployedDetail())
    expect(writerPage.text()).toContain('当前身份可以看懂下一步，但不能提交部署或发布')
    expect(buttonByText(writerPage, '开始部署')?.attributes('disabled') || 'missing').not.toBeUndefined()
    writerPage.unmount()

    mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
    const wrapper = await mountPage(undeployedDetail())
    const guide = await openGuide(wrapper)
    const groupSelect = findControl(guide, ['plugin-guide-resource-group', 'deployment-resource-group'])
    expect(groupSelect).toBeTruthy()
    expect(Array.from(groupSelect.element.options).map((option) => option.value)).toEqual(['default', 'team', 'hidden'])
    const targets = guide.findAll('[data-test="plugin-guide-target"], [data-test="deployment-agent"]')
    expect(targets.map((input) => input.element.value).sort()).toEqual(['edge-a', 'edge-b'])
    expect(guide.findAll('.plugin-deployment__agent input[type="checkbox"]').length).toBe(0)
    expect(guide.find('input[data-test="deployment-resource-group"]').exists()).toBe(false)
  })

  it('renders rule_ref as a select of host HTTP rules and blocks an empty required value', async () => {
    const schema = {
      type: 'object',
      required: ['rule_ref'],
      properties: {
        rule_ref: { type: 'string', title: '规则', minLength: 1, maxLength: 128 },
        note: { type: 'string', title: '备注' }
      }
    }
    const visibleRules = [
      { id: 12, frontend_url: 'https://media.example.com', name: 'media', agent_id: 'edge-a', agent_name: 'Edge A' },
      { id: 13, frontend_url: 'https://tv.example.com', name: 'tv', agent_id: 'edge-b', agent_name: 'Edge B' }
    ]
    mocks.fetchHttpRulesPage.mockResolvedValue({ items: visibleRules, total: 2 })

    const undeployed = await mountPage(undeployedDetail({
      package: { ...makeDetail().package, config_schema: schema }
    }))
    const deploy = await openGuide(undeployed)
    await flushPromises()
    const deployRule = deploy.findAll('.declarative-field').find((field) => field.text().includes('规则'))
    expect(deployRule).toBeTruthy()
    expect(deployRule.find('select').exists()).toBe(true)
    expect(deployRule.find('input[type="text"]').exists()).toBe(false)
    expect(Array.from(deployRule.get('select').element.options).map((option) => option.value)).toEqual([
      'https://media.example.com',
      'https://tv.example.com'
    ])
    undeployed.unmount()

    const empty = await mountPage(makeDetail({
      package: { ...makeDetail().package, config_schema: schema },
      instances: [makeInstance({ config: {} })]
    }))
    mocks.fetchHttpRulesPage.mockResolvedValue({ items: [], total: 0 })
    const emptyModal = await openConfigModal(empty)
    await flushPromises()
    expect(emptyModal.text()).toMatch(/当前没有可见的 HTTP 规则/)
    await modalButton(emptyModal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).not.toHaveBeenCalled()
    empty.unmount()

    mocks.fetchHttpRulesPage.mockResolvedValue({ items: visibleRules, total: 2 })
    const wrapper = await mountPage(makeDetail({
      package: { ...makeDetail().package, config_schema: schema },
      instances: [makeInstance({ config: {} })]
    }))
    const modal = await openConfigModal(wrapper)
    await flushPromises()
    const ruleField = modal.findAll('.declarative-field').find((field) => field.text().includes('规则'))
    expect(ruleField).toBeTruthy()
    expect(ruleField.find('select').exists()).toBe(true)
    expect(ruleField.find('input[type="text"]').exists()).toBe(false)
    expect(Array.from(ruleField.get('select').element.options).map((option) => option.value)).toEqual([
      'https://media.example.com',
      'https://tv.example.com'
    ])
    await ruleField.get('select').setValue('https://media.example.com')
    await modalButton(modal, '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.configurePlugin).toHaveBeenCalledWith('official.waf', expect.objectContaining({
      config: expect.objectContaining({ rule_ref: 'https://media.example.com' })
    }))
  })

  it('keeps ops off the first screen after the plugin is already deployed', async () => {
    const wrapper = await mountPage()
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toBe('已部署')
    expect(firstScreenButtons(wrapper).some((button) => button.text() === '编辑配置')).toBe(true)
    expect(wrapper.find('[data-test="plugin-task-center"] .declarative-field').exists()).toBe(false)
    expectOpsOnlyInMore(wrapper)
    const more = await openMore(wrapper)
    expect(more.text()).toContain('逐 Agent 状态')
    expect(more.text()).toMatch(/运行日志/)
    expect(more.text()).toMatch(/生命周期操作与审计/)
    expect(buttonByText(more, '导出脱敏诊断')).toBeTruthy()
  })
})
