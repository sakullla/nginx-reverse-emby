import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginDetailPage from './PluginDetailPage.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  fetchAgents: vi.fn(),
  fetchRules: vi.fn(),
  fetchHttpRulesPage: vi.fn(),
  refreshActor: vi.fn(),
  actor: { permissions: ['*'], visible_resource_groups: [] }
}))
vi.mock('../../api/client', () => ({ api: { get: mocks.get, post: mocks.post }, longRunningRequest: { timeout: 0 } }))
vi.mock('../../api', () => ({
  fetchAgents: mocks.fetchAgents,
  fetchRules: mocks.fetchRules,
  fetchHttpRulesPage: mocks.fetchHttpRulesPage
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: 'rpc.plugin' } }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('../../api/operations', () => ({ retryRevision: vi.fn() }))
vi.mock('../../context/useAccessControl', async (original) => {
  const actual = await original()
  return {
    ...actual,
    useAccessControl: () => ({
      actor: { value: mocks.actor },
      can: (permission) => mocks.actor.permissions.includes('*') || mocks.actor.permissions.includes(permission),
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
    template: '<div v-if="modelValue" class="base-modal-stub" :data-test="dataTest"><button type="button" class="modal__close" data-test="modal-close" @click="$emit(\'update:modelValue\', false)">关闭</button><slot /><slot name="footer" /></div>'
  }
}))

function buttonByText(wrapper, text) {
  return wrapper.findAll('button').find((button) => button.text() === text)
}

function configModal(wrapper) {
  return wrapper.find('[data-test="plugin-instance-config-modal"]')
}

async function openConfigModal(wrapper) {
  await wrapper.findAll('button').find((button) => button.text() === '编辑配置').trigger('click')
  return wrapper.get('[data-test="plugin-instance-config-modal"]')
}

const OPS_ACTION_LABELS = ['启用', '停用', '回滚', '卸载', '导出脱敏诊断']
const OPS_SECTION_PATTERNS = [/Agent 执行面状态|逐 Agent 状态/, /运行日志/, /生命周期操作与审计|操作时间线|审计/]

function isOverlay(element) {
  return Boolean(element.closest('[data-test="plugin-deploy-modal"], [data-test="plugin-instance-config-modal"], .base-modal-stub, .delete-dialog-stub'))
}

function isInMore(element) {
  return Boolean(element.closest('[data-test="plugin-more"]'))
}

function pagePrimaryButtons(wrapper) {
  return wrapper.findAll('button.btn-primary').filter((button) => {
    return !isOverlay(button.element) && !isInMore(button.element) && button.isVisible()
  })
}

function mainPathButtons(wrapper) {
  return wrapper.findAll('button').filter((button) => !isOverlay(button.element) && !isInMore(button.element))
}

function mainPathText(wrapper) {
  const parts = []
  const header = wrapper.find('.page-header')
  if (header.exists()) parts.push(header.text())
  const task = wrapper.find('[data-test="plugin-task-center"]')
  if (task.exists()) parts.push(task.text())
  for (const section of wrapper.findAll('.plugin-section')) {
    if (!isInMore(section.element) && !isOverlay(section.element)) parts.push(section.text())
  }
  return parts.join('\n')
}

function morePanel(wrapper) {
  return wrapper.find('[data-test="plugin-more"]')
}

async function openMore(wrapper) {
  const panel = morePanel(wrapper)
  expect(panel.exists()).toBe(true)
  if (!panel.element.open) {
    const summary = panel.find('summary')
    if (summary.exists()) await summary.trigger('click')
    else panel.element.open = true
  }
  return panel
}

function moreButton(wrapper, text) {
  return morePanel(wrapper).findAll('button').find((button) => button.text() === text)
}

function mainPathConfigFields(wrapper) {
  return wrapper.findAll('.declarative-field, .declarative-ui').filter((node) => !isOverlay(node.element) && !isInMore(node.element))
}

function visibleHttpRules() {
  return [
    { id: 21, name: 'media', frontend: 'https://media.example.com', frontend_url: 'https://media.example.com', agent_id: 'edge-a', enabled: true },
    { id: 22, name: 'tv', frontend: 'https://tv.example.com', frontend_url: 'https://tv.example.com', agent_id: 'edge-b', enabled: true }
  ]
}

function taskGuide(wrapper) {
  const guide = wrapper.find('[data-test="plugin-task-guide"]')
  if (guide.exists()) return guide
  const modal = wrapper.find('[data-test="plugin-deploy-modal"]')
  if (modal.exists()) return modal
  return wrapper
}

function publishedSurface(wrapper) {
  const single = wrapper.find('[data-test="plugin-published-entry"]')
  if (single.exists()) return single
  return wrapper.find('[data-test="plugin-published-entries"]')
}

function findControl(root, testIds, labelPattern) {
  for (const id of testIds) {
    const node = root.find(`[data-test="${id}"]`)
    if (!node.exists()) continue
    if (['INPUT', 'SELECT', 'TEXTAREA'].includes(node.element.tagName)) return node
    const inner = node.find('input, select, textarea')
    return inner.exists() ? inner : node
  }
  if (!labelPattern) return null
  const label = root.findAll('label').find((node) => labelPattern.test(node.text()))
  if (!label) return null
  const inner = label.find('input, select, textarea')
  return inner.exists() ? inner : null
}

async function chooseTarget(guide, agentId) {
  const named = findControl(guide, ['plugin-guide-target', 'deployment-target', 'plugin-deploy-target'])
  if (named && named.element.tagName === 'SELECT') {
    await named.setValue(agentId)
    return
  }
  const match = guide.findAll('[data-test="plugin-guide-target"], [data-test="deployment-agent"], input[type="radio"], .plugin-deployment__agent input, .plugin-guide__agent input').find((input) => input.element.value === agentId)
  if (!match) throw new Error(`deployment target ${agentId} was not rendered`)
  if (match.element.type === 'checkbox' || match.element.type === 'radio') await match.setChecked(true)
  else await match.setValue(agentId)
}

async function fillPublishedEntry(guide, host, https) {
  const hostNode = findControl(guide, ['plugin-guide-domain', 'deployment-domain', 'deployment-frontend-host', 'plugin-publish-domain'], /入口域名|^域名$/)
  const urlNode = findControl(guide, ['deployment-frontend-url', 'plugin-publish-frontend-url'], /访问地址|frontend/)
  const httpsNode = findControl(guide, ['plugin-guide-https', 'deployment-https', 'plugin-publish-https'], /HTTPS|https/)
  if (hostNode) await hostNode.setValue(host)
  else if (urlNode) await urlNode.setValue(`${https ? 'https' : 'http'}://${host}`)
  else throw new Error('entry domain field was not rendered')
  if (!httpsNode) {
    if (urlNode) return
    throw new Error('HTTPS field was not rendered')
  }
  if (httpsNode.element.tagName === 'SELECT') {
    const options = Array.from(httpsNode.element.options || []).map((option) => option.value)
    if (options.some((value) => String(value).includes('://'))) await httpsNode.setValue(https ? 'https://' : 'http://')
    else await httpsNode.setValue(https ? 'https' : 'http')
    return
  }
  if (httpsNode.element.type === 'checkbox' || httpsNode.element.type === 'radio') await httpsNode.setValue(https)
  else await httpsNode.setValue(https)
}

function guideSubmitButton(guide) {
  const named = guide.find('[data-test="plugin-task-primary"]')
  if (named.exists()) return named
  return ['发布到域名', '部署并发布', '保存入口', '发布', '开始部署', '部署']
    .map((label) => buttonByText(guide, label))
    .find(Boolean) || guide.find('button.btn-primary')
}

function writePaths() {
  return mocks.post.mock.calls.map((call) => String(call[0] || ''))
}

function httpBackendManifest(overrides = {}) {
  return {
    id: 'rpc.plugin',
    name: 'RPC Plugin',
    description: '把媒体站发布到一个节点',
    extension_points: ['http.backend-provider'],
    http_backend_providers: [{ id: 'default', display_name: 'Default' }],
    ...overrides
  }
}

function productionDetail(overrides = {}) {
  return {
    plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'disabled', desired_lifecycle: 'disabled', active_source_kind: 'official' },
    package: {
      manifest: httpBackendManifest(),
      runtime: { kind: 'rpc-service' },
      artifacts: [],
      permissions: [],
      permission_diff: { added: [], removed: [] },
      config_schema: { type: 'object', properties: { mode: { type: 'string', title: '模式' } } }
    },
    faces: [
      { face_id: 'local-management', host_scope: 'control-plane' },
      { face_id: 'agent-execution', host_scope: 'agent' }
    ],
    target_eligibility: { canonical_local_target_id: 'local-control', agent_targets_allowed: true },
    instances: [],
    agent_statuses: [],
    published_entries: [],
    ...overrides
  }
}

function deployedInstance(overrides = {}) {
  return {
    id: 'rpc.plugin-default',
    resource_group_id: 'default',
    targets: ['edge-a'],
    policy_chains: [],
    bindings: [],
    config: { mode: 'observe' },
    config_version: 1,
    current_state: 'active',
    ...overrides
  }
}

function stubReads(detail) {
  mocks.get.mockImplementation(async (path) => {
    if (path.endsWith('/operations')) {
      return { data: { operations: [{ id: 'op-audit', kind: 'configure', status: 'succeeded', actor_id: 'admin', created_at: '2026-08-17T00:00:00Z' }] } }
    }
    if (path.includes('/access/resource-groups')) {
      return { data: { resource_groups: [{ id: 'default', name: '默认组' }, { id: 'group-a', name: '组 A' }] } }
    }
    if (path.includes('/http-rules') || /\/agents\/[^/]+\/rules$/.test(String(path))) {
      return { data: { items: visibleHttpRules(), rules: visibleHttpRules() } }
    }
    if (path.includes('/logs')) return { data: { entries: [{ created_at: '2026-08-17T00:00:00Z', agent_id: 'edge-a', message: 'ready' }], next_cursor: '' } }
    return { data: detail }
  })
}

async function mountDetail(detail) {
  stubReads(detail)
  mocks.fetchAgents.mockResolvedValue([
    { id: 'edge-a', name: 'Edge A', status: 'online' },
    { id: 'edge-b', name: 'Edge B', status: 'online' }
  ])
  mocks.fetchRules.mockResolvedValue(visibleHttpRules())
  mocks.fetchHttpRulesPage.mockResolvedValue({ items: visibleHttpRules(), total: 2 })
  const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}

async function openTaskGuide(wrapper, actionLabel) {
  const existing = wrapper.find('[data-test="plugin-task-guide"]')
  if (existing.exists() && ['开始部署', '发布到域名'].includes(actionLabel)) return existing
  const button = buttonByText(wrapper, actionLabel)
  if (!button) throw new Error(`${actionLabel} was not rendered`)
  await button.trigger('click')
  await flushPromises()
  return taskGuide(wrapper)
}

describe('PluginDetailPage production API projection', () => {
  beforeEach(() => {
    mocks.get.mockReset()
    mocks.post.mockReset()
    mocks.fetchAgents.mockReset()
    mocks.fetchRules.mockReset()
    mocks.fetchHttpRulesPage.mockReset()
    mocks.refreshActor.mockReset()
    mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
    mocks.fetchRules.mockResolvedValue(visibleHttpRules())
    mocks.fetchHttpRulesPage.mockResolvedValue({ items: visibleHttpRules(), total: 2 })
  })

  it('keeps schema and handle metadata through the real API adapter', async () => {
    mocks.fetchAgents.mockResolvedValue([{ id: 'edge-a', name: 'Edge A', status: 'online' }])
    mocks.get.mockImplementation(async (path) => {
      if (path.endsWith('/operations')) return { data: { operations: [] } }
      if (path.includes('/access/resource-groups')) return { data: { resource_groups: [{ id: 'default', name: '默认组' }, { id: 'group-a', name: '组 A' }] } }
      return { data: {
        plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
        package: {
          manifest: { id: 'rpc.plugin', name: 'RPC Plugin' }, runtime: { kind: 'rpc-service' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] },
          config_schema: { type: 'object', required: ['credential'], properties: {
            token: { type: 'string', title: '普通 Token' },
            credential: { type: 'string', title: 'Credential', writeOnly: true, default: 'package-secret' },
            optional: { type: 'string', title: 'Optional', writeOnly: true }
          } }
        },
        instances: [{
          id: 'instance-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { token: 'active-value' }, secret_fields: [{ pointer: '/credential', present: false }], pending_operation_id: 'configure-pending',
          pending_config: { token: 'ordinary-value', credential: 'server-plaintext', optional: 'other-plaintext' },
          pending_secret_fields: [{ pointer: '/credential', present: true }, { pointer: '/optional', present: false }],
          config_version: 1, current_state: 'active'
        }, {
          id: 'instance-b', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { token: 'active-ordinary', credential: 'active-plaintext', optional: 'optional-plaintext' },
          secret_fields: [{ pointer: '/credential', present: false }, { pointer: '/optional', present: true }],
          config_version: 2, current_state: 'active'
        }],
        agent_statuses: []
      } }
    })
    mocks.post.mockResolvedValue({ data: { result: {} } })
    const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    expect(wrapper.text()).not.toContain('普通 Token')
    expect(wrapper.html()).not.toContain('server-plaintext')
    expect(wrapper.html()).not.toContain('other-plaintext')
    expect(wrapper.html()).not.toContain('package-secret')
    const pendingModal = await openConfigModal(wrapper)
    expect(pendingModal.text()).toContain('普通 Token')
    expect(pendingModal.get('.declarative-field input[type="text"]').element.value).toBe('ordinary-value')
    expect(pendingModal.get('.declarative-field input[type="password"]').attributes('required')).toBeUndefined()
    expect(pendingModal.text()).toContain('已有凭据')
    expect(pendingModal.findAll('button').filter((button) => button.text() === '清除凭据')).toHaveLength(0)

    await pendingModal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      config: { token: 'ordinary-value' }, secret_replacements: {}
    }), { timeout: 0 })
    expect(configModal(wrapper).exists()).toBe(false)

    await wrapper.findAll('[role="tab"]').find((tab) => tab.text().includes('instance-b')).trigger('click')
    await flushPromises()
    const activeModal = await openConfigModal(wrapper)
    const activeSecretInputs = activeModal.findAll('.declarative-field input[type="password"]')
    expect(activeModal.get('.declarative-field input[type="text"]').element.value).toBe('active-ordinary')
    expect(activeModal.html()).not.toContain('active-plaintext')
    expect(activeModal.html()).not.toContain('optional-plaintext')
    expect(activeSecretInputs[0].attributes('required')).toBeDefined()
    expect(activeModal.findAll('button').filter((button) => button.text() === '清除凭据')).toHaveLength(1)
  })

  it('saves a nested schema fallback form and blocks invalid configure posts', async () => {
    mocks.fetchAgents.mockResolvedValue([{ id: 'edge-a', name: 'Edge A', status: 'online' }])
    mocks.get.mockImplementation(async (path) => {
      if (path.endsWith('/operations')) return { data: { operations: [] } }
      if (path.includes('/access/resource-groups')) return { data: { resource_groups: [{ id: 'default', name: '默认组' }] } }
      return { data: {
        plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
        package: {
          manifest: { id: 'rpc.plugin', name: 'RPC Plugin' }, runtime: { kind: 'rpc-service' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] },
          config_schema: {
            type: 'object',
            required: ['region'],
            properties: {
              region: { type: 'string', title: '区域', minLength: 2 },
              sources: {
                type: 'array',
                title: '源',
                items: { type: 'object', required: ['host'], properties: { host: { type: 'string', title: '主机', minLength: 2 } } }
              }
            }
          }
        },
        instances: [{
          id: 'instance-a', resource_group_id: 'default', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { region: 'X', sources: [{ host: 'a' }] },
          config_version: 1, current_state: 'active'
        }],
        agent_statuses: []
      } }
    })
    mocks.post.mockResolvedValue({ data: { result: {} } })
    const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    const modal = await openConfigModal(wrapper)
    await modal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(modal.text()).toContain('至少 2 个字符')

    const inputs = modal.findAll('.declarative-field input[type="text"]')
    await inputs[0].setValue('eu')
    await inputs[1].setValue('edge.example')
    await modal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      config: { region: 'eu', sources: [{ host: 'edge.example' }] }
    }), { timeout: 0 })
  })

  it('lets a resource writer persist an existing instance through the real configure adapter', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['group-a'] }
    mocks.fetchAgents.mockResolvedValue([{ id: 'edge-a', name: 'Edge A', status: 'online' }])
    mocks.get.mockImplementation(async (path) => {
      if (path.endsWith('/operations')) return { data: { operations: [] } }
      if (path.includes('/access/resource-groups')) return { data: { resource_groups: [{ id: 'group-a', name: '组 A' }] } }
      return { data: {
        plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
        package: {
          manifest: { id: 'rpc.plugin', name: 'RPC Plugin' }, runtime: { kind: 'rpc-service' }, artifacts: [], permissions: [], permission_diff: { added: [], removed: [] },
          config_schema: { type: 'object', required: ['mode'], properties: { mode: { type: 'string', title: '模式' } } }
        },
        instances: [{
          id: 'instance-a', resource_group_id: 'group-a', targets: ['edge-a'], policy_chains: [], bindings: [],
          config: { mode: 'observe' }, config_version: 1, current_state: 'active'
        }],
        agent_statuses: []
      } }
    })
    mocks.post.mockResolvedValue({ data: { result: {} } })
    const wrapper = mount(PluginDetailPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()

    const modal = await openConfigModal(wrapper)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      instance_id: 'instance-a',
      resource_group_id: 'group-a',
      config: { mode: 'block' }
    }), { timeout: 0 })
  })

  it('does not write projected http_rule bindings back through configure after HTTP publish', async () => {
    mocks.actor = { permissions: ['resource.write'], visible_resource_groups: ['default'] }
    mocks.post.mockResolvedValue({ data: { result: {} } })
    const wrapper = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance({
        bindings: [{ consumer: { kind: 'http_rule', id: '12' }, target_agent_id: 'edge-a' }]
      })],
      published_entries: [{
        rule_id: 12,
        agent_id: 'edge-a',
        frontend_url: 'https://media.example.com',
        enabled: true,
        accessible: true
      }]
    }))

    const modal = await openConfigModal(wrapper)
    expect(modal.find('[data-test="plugin-publish-needed"]').exists()).toBe(false)
    expect(modal.text()).not.toContain('还差发布')
    expect(modal.get('[data-test="plugin-published-entry"]').text()).toContain('https://media.example.com')
    expect(modal.find('[data-test="plugin-publish-submit"]').exists()).toBe(false)
    await modal.get('.declarative-field input[type="text"]').setValue('block')
    await modal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      instance_id: 'rpc.plugin-default',
      bindings: [],
      config: { mode: 'block' }
    }), { timeout: 0 })
    expect(writePaths()).toEqual(['/plugins/rpc.plugin/configure'])
  })
})

describe('PluginDetailPage task-center production API projection', () => {
  beforeEach(() => {
    mocks.get.mockReset()
    mocks.post.mockReset()
    mocks.fetchAgents.mockReset()
    mocks.fetchRules.mockReset()
    mocks.fetchHttpRulesPage.mockReset()
    mocks.refreshActor.mockReset()
    mocks.actor = { permissions: ['*'], visible_resource_groups: [] }
    mocks.fetchRules.mockResolvedValue(visibleHttpRules())
    mocks.fetchHttpRulesPage.mockResolvedValue({ items: visibleHttpRules(), total: 2 })
  })

  it('keeps 开始部署 as the first-screen primary action before anything is deployed', async () => {
    const wrapper = await mountDetail(productionDetail())
    expect(wrapper.find('.plugin-task__purpose').text()).toContain('把媒体站发布到一个节点')
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toBe('还没部署')
    expect(pagePrimaryButtons(wrapper).map((button) => button.text())).toEqual(['开始部署'])
    expect(mainPathConfigFields(wrapper)).toHaveLength(0)
    expect(wrapper.find('[data-test="plugin-deploy-modal"]').exists()).toBe(false)
    expect(mainPathButtons(wrapper).map((button) => button.text()).filter((text) => OPS_ACTION_LABELS.includes(text))).toEqual(['卸载'])
    for (const pattern of OPS_SECTION_PATTERNS) {
      expect(mainPathText(wrapper)).not.toMatch(pattern)
    }

    await buttonByText(wrapper, '开始部署').trigger('click')
    await flushPromises()
    const modal = wrapper.get('[data-test="plugin-deploy-modal"]')
    expect(modal.exists()).toBe(true)
    expect(findControl(modal, ['plugin-guide-resource-group', 'deployment-resource-group'])).toBeTruthy()
    expect(modal.text()).toContain('默认组')
    expect(modal.text()).toContain('组 A')
    expect(modal.text()).toContain('Edge A')
    expect(modal.text()).toContain('Edge B')
    expect(mainPathConfigFields(wrapper)).toHaveLength(0)
  })

  it('prompts to finish publishing only when an HTTP backend is already deployed', async () => {
    const unpublished = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance()]
    }))
    expect(unpublished.text()).toContain('还没发布域名')
    expect(pagePrimaryButtons(unpublished).some((button) => button.text() === '发布到域名')).toBe(true)
    unpublished.unmount()

    const noHttp = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      package: {
        ...productionDetail().package,
        manifest: { id: 'rpc.plugin', name: 'RPC Plugin' }
      },
      instances: [deployedInstance()]
    }))
    expect(noHttp.text()).toContain('已部署')
    expect(noHttp.text()).not.toContain('还没发布域名')
    expect(buttonByText(noHttp, '发布到域名')).toBeUndefined()
  })

  it('guides exactly one node plus a required domain and HTTPS without a rules-page detour', async () => {
    const wrapper = await mountDetail(productionDetail())
    const guide = await openTaskGuide(wrapper, '开始部署')
    expect(guide.text()).not.toContain('到 HTTP 规则添加')
    expect(guide.text()).not.toContain('选择插件提供商')
    expect(guide.text()).not.toContain('全选')
    expect(guide.findAll('.plugin-deployment__agent input[type="radio"]').length).toBeGreaterThan(0)
    expect(guide.findAll('.plugin-deployment__agent input[type="checkbox"]').length).toBe(0)
    expect(guide.find('[data-test="plugin-guide-domain"], [data-test="deployment-domain"], [data-test="deployment-frontend-host"]').exists()).toBe(true)
    expect(guide.find('[data-test="plugin-guide-https"], [data-test="deployment-https"]').exists()).toBe(true)
    expect(guideSubmitButton(guide).attributes('disabled')).toBeDefined()

    await chooseTarget(guide, 'edge-a')
    expect(guideSubmitButton(guide).attributes('disabled')).toBeDefined()
    await fillPublishedEntry(guide, 'media.example.com', true)
    expect(guideSubmitButton(guide).attributes('disabled')).toBeUndefined()
    const checked = guide.findAll('input[type="radio"]:checked, input[type="checkbox"]:checked').filter((input) => ['edge-a', 'edge-b'].includes(input.element.value))
    if (checked.length) expect(checked).toHaveLength(1)
  })

  it('publishes one node and one enabled HTTP entry through the real plugin adapter', async () => {
    const published = {
      rule_id: 12,
      agent_id: 'edge-a',
      frontend_url: 'https://media.example.com',
      enabled: true,
      accessible: true
    }
    mocks.post.mockResolvedValue({
      data: {
        result: {
          instance: { ...deployedInstance(), bindings: [{ consumer: { kind: 'http_rule', id: '12' }, target_agent_id: 'edge-a' }] },
          published_entries: [published]
        }
      }
    })
    const wrapper = await mountDetail(productionDetail())
    const guide = await openTaskGuide(wrapper, '开始部署')
    await chooseTarget(guide, 'edge-a')
    await fillPublishedEntry(guide, 'media.example.com', true)
    const mode = guide.find('.declarative-field input[type="text"]')
    if (mode.exists()) await mode.setValue('observe')
    await guideSubmitButton(guide).trigger('click')
    await flushPromises()

    expect(writePaths()).toEqual(['/plugins/rpc.plugin/publish'])
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/publish', expect.objectContaining({
      targets: ['edge-a'],
      frontend_url: 'https://media.example.com'
    }), { timeout: 0 })
    const body = mocks.post.mock.calls[0][1]
    expect(body).not.toHaveProperty('provider_id')
    expect(body).not.toHaveProperty('backends')
    expect(body).not.toHaveProperty('rule_id')
    expect(writePaths().some((path) => path.includes('/rules') || path.endsWith('/configure'))).toBe(false)
  })

  it('stays undeployed or unpublished after leaving mid-guide and shows the published host once complete', async () => {
    const undeployed = await mountDetail(productionDetail())
    await openTaskGuide(undeployed, '开始部署')
    undeployed.unmount()
    const reopened = await mountDetail(productionDetail())
    expect(reopened.text()).toContain('还没部署')
    expect(reopened.text()).not.toContain('已可用')
    reopened.unmount()

    const unpublished = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance()]
    }))
    expect(unpublished.text()).toContain('还没发布域名')
    expect(unpublished.text()).not.toContain('已可用')
    unpublished.unmount()

    const published = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance({
        bindings: [{ consumer: { kind: 'http_rule', id: '12' }, target_agent_id: 'edge-a' }]
      })],
      published_entries: [{
        rule_id: 12,
        agent_id: 'edge-a',
        frontend_url: 'https://user:secret@media.example.com',
        enabled: true,
        accessible: true
      }]
    }))
    expect(publishedSurface(published).text()).toContain('https://user:[REDACTED]@media.example.com')
    expect(published.text()).toContain('已可用')
    expect(published.html()).not.toContain('user:secret@media.example.com')
  })

  it('updates the original published entry through plugin publish instead of the rules page', async () => {
    mocks.post.mockResolvedValue({
      data: {
        result: {
          published_entries: [{
            rule_id: 12,
            agent_id: 'edge-a',
            frontend_url: 'https://media-v2.example.com',
            enabled: true,
            accessible: true
          }]
        }
      }
    })
    const wrapper = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance({
        bindings: [{ consumer: { kind: 'http_rule', id: '12' }, target_agent_id: 'edge-a' }]
      })],
      published_entries: [{
        rule_id: 12,
        agent_id: 'edge-a',
        frontend_url: 'https://media.example.com',
        enabled: true,
        accessible: false
      }]
    }))
    expect(wrapper.text()).toContain('已发布但还不能访问')
    expect(publishedSurface(wrapper).text()).toContain('https://media.example.com')

    const guide = await openTaskGuide(wrapper, '修改入口')
    await fillPublishedEntry(guide, 'media-v2.example.com', true)
    await guideSubmitButton(guide).trigger('click')
    await flushPromises()

    expect(writePaths()).toEqual(['/plugins/rpc.plugin/publish'])
    expect(mocks.post.mock.calls[0][1]).toEqual(expect.objectContaining({
      rule_id: 12,
      targets: ['edge-a'],
      frontend_url: 'https://media-v2.example.com'
    }))
    expect(writePaths().some((path) => path.includes('/rules'))).toBe(false)
  })

  it('publishes another domain as a separate plugin-side entry', async () => {
    mocks.post.mockResolvedValue({
      data: {
        result: {
          published_entries: [{
            rule_id: 13,
            agent_id: 'edge-b',
            frontend_url: 'https://alt.example.com',
            enabled: true,
            accessible: true
          }]
        }
      }
    })
    const wrapper = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance({
        bindings: [{ consumer: { kind: 'http_rule', id: '12' }, target_agent_id: 'edge-a' }]
      })],
      published_entries: [{
        rule_id: 12,
        agent_id: 'edge-a',
        frontend_url: 'https://media.example.com',
        enabled: true,
        accessible: true
      }]
    }))
    const extraLabel = buttonByText(wrapper, '发布另一域名') ? '发布另一域名' : '再发布一条域名'
    const guide = await openTaskGuide(wrapper, extraLabel)
    await chooseTarget(guide, 'edge-b')
    await fillPublishedEntry(guide, 'alt.example.com', true)
    await guideSubmitButton(guide).trigger('click')
    await flushPromises()
    expect(writePaths()).toEqual(['/plugins/rpc.plugin/publish'])
    expect(mocks.post.mock.calls[0][1]).toEqual(expect.objectContaining({
      targets: ['edge-b'],
      frontend_url: 'https://alt.example.com'
    }))
    expect(mocks.post.mock.calls[0][1]).not.toHaveProperty('rule_id')
  })

  it('deletes a published entry from the plugin page without a rules-page detour', async () => {
    mocks.post.mockResolvedValue({ data: { result: { published_entries: [] } } })
    const wrapper = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance({
        bindings: [{ consumer: { kind: 'http_rule', id: '12' }, target_agent_id: 'edge-a' }]
      })],
      published_entries: [{
        rule_id: 12,
        agent_id: 'edge-a',
        frontend_url: 'https://media.example.com',
        enabled: true,
        accessible: true
      }]
    }))
    await buttonByText(wrapper, '删除入口').trigger('click')
    expect(wrapper.find('.delete-dialog-title').text()).toBe('确认删除入口')
    await wrapper.find('.delete-dialog-confirm').trigger('click')
    await flushPromises()
    expect(writePaths()).toEqual(['/plugins/rpc.plugin/unpublish'])
    expect(mocks.post.mock.calls[0][1]).toEqual({
      rule_id: 12,
      targets: ['edge-a']
    })
    expect(writePaths().some((path) => path.includes('/rules'))).toBe(false)
  })

  it('keeps lifecycle, diagnostics, logs, and timeline only inside 更多', async () => {
    const wrapper = await mountDetail(productionDetail({
      plugin: {
        plugin_id: 'rpc.plugin',
        current_lifecycle: 'active',
        rollback_package_digest: 'sha256:rollback',
        active_source_kind: 'official'
      },
      instances: [deployedInstance()],
      agent_statuses: [{
        instance_id: 'rpc.plugin-default',
        agent_id: 'edge-a',
        target_scope: 'active',
        runtime_state: 'failed',
        desired_revision: 2,
        target_revision: 2,
        current_revision: 1,
        operation_kind: 'configure',
        operation_status: 'failed'
      }]
    }))
    expect(wrapper.get('[data-test="plugin-task-status"]').text()).toBe('还没发布域名')
    expect(buttonByText(wrapper, '发布到域名')).toBeTruthy()
    expect(mainPathConfigFields(wrapper)).toHaveLength(0)
    expect(mainPathButtons(wrapper).map((button) => button.text()).filter((text) => OPS_ACTION_LABELS.includes(text))).toEqual([])
    for (const pattern of OPS_SECTION_PATTERNS) {
      expect(mainPathText(wrapper)).not.toMatch(pattern)
    }

    const more = await openMore(wrapper)
    expect(more.get('summary').text()).toBe('更多')
    for (const label of OPS_ACTION_LABELS) {
      expect(moreButton(wrapper, label)).toBeTruthy()
    }
    expect(more.text()).toContain('Agent 执行面状态')
    expect(more.text()).toMatch(/运行日志/)
    expect(more.text()).toMatch(/生命周期操作与审计|操作时间线|审计/)
    expect(more.text()).toContain('Edge A')
    expect(more.text()).toContain('ready')
    expect(more.text()).toContain('configure')
    expect(more.find('.plugin-technical').exists()).toBe(true)

    await moreButton(wrapper, '停用').trigger('click')
    expect(wrapper.find('.delete-dialog-stub').exists()).toBe(true)
    await wrapper.find('.delete-dialog-cancel').trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    wrapper.unmount()

    mocks.actor = { permissions: ['resource.read'], visible_resource_groups: ['default'] }
    const readonlyPage = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance()]
    }))
    expect(readonlyPage.text()).toContain('还没发布域名')
    expect(mainPathConfigFields(readonlyPage)).toHaveLength(0)
    const publish = buttonByText(readonlyPage, '发布到域名')
    if (publish) {
      expect(publish.attributes('disabled')).toBeDefined()
      await publish.trigger('click')
    }
    expect(configModal(readonlyPage).exists()).toBe(false)
    expect(mocks.post).not.toHaveBeenCalled()
  })

  it('edits deployed config only in the modal and does not write when the modal is cancelled', async () => {
    const wrapper = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance({ config: { mode: 'observe' } })]
    }))
    expect(mainPathConfigFields(wrapper)).toHaveLength(0)
    expect(configModal(wrapper).exists()).toBe(false)

    const modal = await openConfigModal(wrapper)
    const input = modal.get('.declarative-field input[type="text"]')
    expect(input.element.value).toBe('observe')
    await input.setValue('block')
    await modal.get('[data-test="modal-close"]').trigger('click')
    await flushPromises()

    expect(configModal(wrapper).exists()).toBe(false)
    expect(mainPathConfigFields(wrapper)).toHaveLength(0)
    expect(mocks.post).not.toHaveBeenCalled()
    expect(writePaths()).toEqual([])
  })

  it('lets the deploy and publish modal pick a visible node and resource group', async () => {
    const wrapper = await mountDetail(productionDetail())
    const guide = await openTaskGuide(wrapper, '开始部署')
    const groupSelect = findControl(guide, ['plugin-guide-resource-group', 'deployment-resource-group'])
    expect(groupSelect).toBeTruthy()
    expect(groupSelect.element.tagName).toBe('SELECT')
    const groupValues = Array.from(groupSelect.element.options || []).map((option) => option.value)
    expect(groupValues).toEqual(['default', 'group-a'])
    expect(guide.text()).toContain('默认组')
    expect(guide.text()).toContain('组 A')
    expect(guide.text()).toContain('Edge A')
    expect(guide.text()).toContain('Edge B')
    expect(guide.text()).not.toContain('edge-hidden')
    await chooseTarget(guide, 'edge-a')
    await groupSelect.setValue('group-a')
    expect(groupSelect.element.value).toBe('group-a')
  })

  it('keeps a control-plane-only plugin on the local management face without a remote target selector', async () => {
    const wrapper = await mountDetail(productionDetail({
      package: {
        ...productionDetail().package,
        manifest: httpBackendManifest({
          runtime: { kind: 'rpc-service', host_scope: 'control-plane' }
        })
      },
      faces: [{ face_id: 'local-management', host_scope: 'control-plane' }],
      target_eligibility: { canonical_local_target_id: 'local-control', agent_targets_allowed: false }
    }))

    const localFace = wrapper.get('[data-test="plugin-face-local-management"]')
    expect(localFace.text()).toMatch(/本地管理面|管理面.*local/i)
    expect(wrapper.find('[data-test="plugin-face-agent-execution"]').exists()).toBe(false)

    const guide = await openTaskGuide(wrapper, '开始部署')
    const remoteTargets = guide.findAll('input, select, option').filter((node) => ['edge-a', 'edge-b'].includes(String(node.element.value || '')))
    expect(remoteTargets).toHaveLength(0)
    expect(guide.text()).not.toContain('Edge A')
    expect(guide.text()).not.toContain('Edge B')
    expect(guide.text()).toMatch(/本地|local-control/i)
  })

  it('separates local management from Agent execution generation and failures for a dual-face plugin', async () => {
    const wrapper = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      instances: [deployedInstance()],
      agent_statuses: [{
        face_id: 'agent-execution',
        instance_id: 'rpc.plugin-default',
        agent_id: 'edge-a',
        target_scope: 'active',
        available: true,
        generation_id: 'generation-edge-a-4',
        runtime_state: 'failed',
        runtime_error_code: 'activation_failed',
        desired_revision: 4,
        current_revision: 3,
        operation_kind: 'configure',
        operation_status: 'failed',
        last_apply_message: 'activation failed on Agent'
      }]
    }))

    const localFace = wrapper.get('[data-test="plugin-face-local-management"]')
    const agentFace = wrapper.get('[data-test="plugin-face-agent-execution"]')
    expect(localFace.text()).toMatch(/本地管理面|管理面.*local/i)
    expect(localFace.text()).not.toContain('activation_failed')
    expect(agentFace.text()).toContain('Edge A')
    expect(agentFace.text()).toContain('Agent 执行面')
    const executionStatus = wrapper.get('[data-test="plugin-agent-execution-status"]')
    expect(executionStatus.text()).toContain('generation-edge-a-4')
    expect(executionStatus.text()).toContain('activation_failed')
    expect(executionStatus.text()).toContain('Agent 执行面')
  })

  it('binds the rule_ref select in the config modal to visible HTTP rules', async () => {
    const wrapper = await mountDetail(productionDetail({
      plugin: { plugin_id: 'rpc.plugin', current_lifecycle: 'active', active_source_kind: 'official' },
      package: {
        ...productionDetail().package,
        config_schema: {
          type: 'object',
          required: ['rule_ref'],
          properties: {
            mode: { type: 'string', title: '模式' },
            rule_ref: { type: 'string', title: '规则', minLength: 1, maxLength: 128 }
          }
        }
      },
      instances: [deployedInstance({ config: { mode: 'observe' } })]
    }))
    const modal = await openConfigModal(wrapper)
    await flushPromises()
    const ruleField = modal.findAll('.declarative-field').find((field) => field.text().includes('规则'))
    expect(ruleField).toBeTruthy()
    expect(ruleField.find('select').exists()).toBe(true)
    expect(ruleField.find('input[type="text"]').exists()).toBe(false)
    const optionValues = Array.from(ruleField.get('select').element.options || []).map((option) => option.value)
    const optionText = Array.from(ruleField.get('select').element.options || []).map((option) => `${option.value} ${option.text}`).join(' ')
    expect(optionValues).toEqual(['https://media.example.com', 'https://tv.example.com'])
    expect(optionText).toMatch(/media/)
    expect(optionText).toMatch(/tv/)
    expect(optionText).not.toContain('hidden-internal')
    expect(optionText).not.toContain('edge-hidden')
    await ruleField.get('select').setValue('https://media.example.com')
    await modal.findAll('button').find((button) => button.text() === '保存配置').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/plugins/rpc.plugin/configure', expect.objectContaining({
      config: expect.objectContaining({ rule_ref: 'https://media.example.com' })
    }), { timeout: 0 })
  })
})
