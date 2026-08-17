<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { fetchAgents } from '../../api'
import { fetchResourceGroups } from '../../api/access'
import {
  configurePlugin,
  deletePluginInstance,
  disablePlugin,
  enablePlugin,
  fetchPluginDetail,
  fetchPluginOperations,
  publishPlugin,
  rollbackPlugin,
  uninstallPlugin
} from '../../api/plugins'
import { safePluginExport, sanitizePluginText, schemaToUIComponents, stripReadOnlyConfigValues, stripWriteOnlyConfigValues } from '../../api/pluginSecurity'
import { retryRevision } from '../../api/operations'
import { filterPluginDetailForActor, pickDefaultResourceGroupID, resourceGroupDisplayName, useAccessControl, visibleResourceGroupsForActor } from '../../context/useAccessControl'
import { getAgentStatus, getAgentStatusLabel } from '../../utils/agentHelpers'
import BaseTabs from '../../components/base/BaseTabs.vue'
import DeleteConfirmDialog from '../../components/DeleteConfirmDialog.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import PluginAgentStatusTable from '../../components/plugins/PluginAgentStatusTable.vue'
import PluginDeclarativeUI from '../../components/plugins/PluginDeclarativeUI.vue'
import PluginDeployModal from '../../components/plugins/PluginDeployModal.vue'
import PluginInstanceConfigModal from '../../components/plugins/PluginInstanceConfigModal.vue'
import PluginLogViewer from '../../components/plugins/PluginLogViewer.vue'
import PluginPackageSummary from '../../components/plugins/PluginPackageSummary.vue'
import PluginRiskNotices from '../../components/plugins/PluginRiskNotices.vue'
import PluginOperationTimeline from '../../components/operations/PluginOperationTimeline.vue'

const route = useRoute()
const router = useRouter()
const { actor, can, refreshActor } = useAccessControl()
const loading = ref(true)
const busy = ref('')
const error = ref('')
const detail = ref(null)
const operations = ref([])
const agents = ref([])
const resourceGroups = ref([])
const selectedInstanceID = ref('')
const retryingAgent = ref('')
const deployModalOpen = ref(false)
const configModalOpen = ref(false)
const guideBusy = ref(false)
const guideError = ref('')
const guide = reactive({
  resourceGroupID: '',
  target: '',
  domain: '',
  https: true,
  editingRuleID: 0,
  extraDomainOpen: false
})

const admin = computed(() => can('*'))
const canWrite = computed(() => admin.value || can('resource.write'))
const selectedInstance = computed(() => detail.value?.instances.find((instance) => instance.id === selectedInstanceID.value) || null)
const selectedConfig = computed(() => selectedInstance.value?.pending_operation_id ? (selectedInstance.value.pending_config || {}) : (selectedInstance.value?.config || {}))
const selectedSecretFields = computed(() => selectedInstance.value?.pending_operation_id
  ? (selectedInstance.value.pending_secret_fields || [])
  : (selectedInstance.value?.secret_fields || []))
const source = computed(() => ({ kind: detail.value?.plugin.active_source_kind, risk_label: detail.value?.plugin.active_source_risk_label }))
const visibleResourceGroups = computed(() => visibleResourceGroupsForActor(resourceGroups.value, actor.value))
const sourceLabel = computed(() => source.value.kind === 'official' ? '官方来源' : '非官方来源')
const lifecycleLabel = computed(() => {
  const labels = { active: '生效中', degraded: '已降级', disabled: '已停用', upgrading: '升级中', applying: '应用中', rolling_back: '回滚中' }
  return labels[detail.value?.plugin?.current_lifecycle] || detail.value?.plugin?.current_lifecycle || '未知'
})
const deploymentStatusLabel = computed(() => (detail.value?.instances || []).length ? '已部署' : '尚未部署')
const refreshIntervalMs = 5000
let refreshTimer = 0
let refreshInFlight = false

const instanceTabs = computed(() => (detail.value?.instances || []).map((instance) => ({
  id: instance.id,
  label: `${instance.id} · ${instance.resource_group_id}`
})))

const isDeclarativeUI = computed(() => !!detail.value?.package?.declarative_ui)
const uiDocument = computed(() => {
  const pkg = detail.value?.package
  if (!pkg) return null
  if (pkg.declarative_ui) return pkg.declarative_ui
  const components = schemaToUIComponents(pkg.config_schema)
  return {
    schema_version: 1,
    title: '',
    components,
    actions: [{ type: 'submit', id: 'save', label: busy.value === 'configure' ? '保存中…' : '保存配置' }]
  }
})
const deploymentDocument = computed(() => ({
  ...(uiDocument.value || {}),
  title: uiDocument.value?.title || '插件配置',
  actions: [{ type: 'submit', id: 'deploy', label: busy.value === 'deploy' ? '部署中…' : '部署' }]
}))
const formConfig = computed(() => {
  const pkg = detail.value?.package
  if (!pkg) return {}
  if (pkg.declarative_ui) return selectedConfig.value
  return stripWriteOnlyConfigValues(pkg.config_schema, selectedConfig.value)
})
const formEmpty = computed(() => !isDeclarativeUI.value && !(uiDocument.value?.components?.length))

const confirmDialog = ref({ visible: false, loading: false, action: '' })
const pluginName = computed(() => detail.value?.package?.manifest?.name || detail.value?.plugin?.plugin_id || '')
const confirmName = computed(() => confirmDialog.value.action === 'delete-instance' ? selectedInstance.value?.id || '' : pluginName.value)
const hasInstances = computed(() => (detail.value?.instances || []).length > 0)
const hasHTTPBackend = computed(() => {
  const pkg = detail.value?.package
  const providers = pkg?.manifest?.http_backend_providers || pkg?.http_backend_providers
  return Array.isArray(providers) && providers.some((provider) => String(provider?.id || '').trim())
})
const publishedEntries = computed(() => {
  const entries = Array.isArray(detail.value?.published_entries) ? detail.value.published_entries : []
  if (admin.value) return entries
  const visibleAgents = new Set()
  for (const instance of detail.value?.instances || []) {
    for (const target of instance.targets || []) {
      if (target) visibleAgents.add(target)
    }
    for (const binding of instance.bindings || []) {
      if (binding?.target_agent_id) visibleAgents.add(binding.target_agent_id)
    }
  }
  return entries.filter((entry) => visibleAgents.has(entry.agent_id))
})
const taskState = computed(() => {
  if (!hasInstances.value) return 'undeployed'
  if (!hasHTTPBackend.value) return 'deployed'
  if (!publishedEntries.value.length) return 'unpublished'
  if (!publishedEntries.value.some((entry) => entry.enabled && entry.accessible)) return 'published-unavailable'
  return 'available'
})
const taskStateLabel = computed(() => ({
  undeployed: '还没部署',
  unpublished: '还没发布域名',
  'published-unavailable': '已发布但还不能访问',
  available: '已可用',
  deployed: '已部署'
}[taskState.value] || ''))
const showHeaderOps = computed(() => taskState.value === 'deployed' || taskState.value === 'available')
const pluginPurpose = computed(() => {
  const description = String(detail.value?.package?.manifest?.description || '').trim()
  if (description) return description
  return hasHTTPBackend.value
    ? '把插件部署到一个节点，再填写一条入口域名即可发布。'
    : '把插件部署到一个节点后即可在该节点上使用。'
})
const taskHint = computed(() => {
  switch (taskState.value) {
    case 'undeployed':
      return '下一步：选择一个节点开始部署。'
    case 'unpublished':
      return '还差发布：填写一条入口域名。'
    case 'published-unavailable':
      return '入口已发布，但现在还不能访问。可在下方查看节点状态并重试。'
    case 'deployed':
      return '插件已部署到所选节点。'
    default:
      return ''
  }
})
const canSubmitGuide = computed(() => taskState.value === 'undeployed' ? admin.value : canWrite.value)
const sortedAgents = computed(() => [...agents.value].sort((left, right) => String(left.name || left.id).localeCompare(String(right.name || right.id))))
const showDomainFields = computed(() => hasHTTPBackend.value && (
  taskState.value === 'undeployed'
  || taskState.value === 'unpublished'
  || guide.editingRuleID > 0
  || guide.extraDomainOpen
))
const showGuideForm = computed(() => (
  taskState.value === 'undeployed'
  || taskState.value === 'unpublished'
  || guide.editingRuleID > 0
  || guide.extraDomainOpen
))
const showNodeFields = computed(() => taskState.value === 'undeployed' || showDomainFields.value)
const showResourceGroup = computed(() => taskState.value === 'undeployed')
const showGuideConfig = computed(() => taskState.value === 'undeployed' && !formEmpty.value)
const guideSubmitLabel = computed(() => {
  if (guideBusy.value) return hasHTTPBackend.value && taskState.value !== 'undeployed' ? '发布中…' : '部署中…'
  if (taskState.value === 'undeployed') return '开始部署'
  return '发布到域名'
})
const guideDocument = computed(() => ({
  ...(uiDocument.value || {}),
  title: uiDocument.value?.title || '插件配置',
  actions: [{ type: 'submit', id: 'task-submit', label: guideSubmitLabel.value }]
}))
const guideBlocker = computed(() => {
  if (!showGuideForm.value) return ''
  if (taskState.value === 'undeployed') {
    if (!visibleResourceGroups.value.length) return '当前身份没有可见的资源组，无法部署。'
    if (!sortedAgents.value.length) return '当前没有可选择的节点。'
  }
  if (showNodeFields.value && !String(guide.target || '').trim()) return '请选择一个节点后再继续。'
  if (showDomainFields.value && !normalizeDomain(guide.domain)) return '请填写一条入口域名。'
  return ''
})
const guideSubmitDisabled = computed(() => (
  guideBusy.value
  || !!busy.value
  || !canSubmitGuide.value
  || !!guideBlocker.value
))

const confirmCopy = computed(() => {
  switch (confirmDialog.value.action) {
    case 'disable':
      return { title: '确认停用插件', message: '停用后该插件将停止处理流量，依赖其防护的流量可能中断；可随时重新启用。', confirmText: '确认停用' }
    case 'rollback':
      return { title: '确认回滚插件', message: '回滚将把插件恢复到上一版本，并可能变更其权限。', confirmText: '确认回滚' }
    case 'delete-instance':
      return { title: '确认删除部署实例', message: '该实例会从所有目标 Agent 下线，配置与插件密钥将被清理；已绑定规则时不能删除。', confirmText: '确认删除实例' }
    case 'uninstall':
    default:
      return { title: '确认卸载插件', message: '卸载将移除插件及其配置，此操作不可撤销。', confirmText: '确认卸载' }
  }
})

onMounted(() => {
  void load()
  refreshTimer = window.setInterval(() => {
    if (busy.value || guideBusy.value || configModalOpen.value || deployModalOpen.value || confirmDialog.value.visible) return
    void load({ background: true })
  }, refreshIntervalMs)
})

onBeforeUnmount(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
})

function normalizeDomain(value) {
  return String(value || '')
    .trim()
    .replace(/^https?:\/\//i, '')
    .replace(/\/.*$/, '')
}

function buildFrontendURL() {
  const domain = normalizeDomain(guide.domain)
  if (!domain) return ''
  return `${guide.https ? 'https' : 'http'}://${domain}`
}

function parseFrontendURL(url) {
  try {
    const parsed = new URL(String(url || ''))
    return { https: parsed.protocol === 'https:', domain: parsed.host }
  } catch {
    const raw = String(url || '').trim()
    return { https: /^https:/i.test(raw), domain: normalizeDomain(raw) }
  }
}

function defaultInstanceID() {
  const pluginID = detail.value?.plugin?.plugin_id || 'plugin'
  const normalized = String(pluginID).toLowerCase().replace(/[^a-z0-9._:/-]+/g, '-').replace(/^[^a-z0-9]+/, '') || 'plugin'
  const used = new Set((detail.value?.instances || []).map((instance) => instance.id))
  const first = `${normalized}-default`.slice(0, 128)
  if (!used.has(first)) return first
  for (let index = 2; index < 10000; index += 1) {
    const suffix = `-${index}`
    const candidate = `${normalized.slice(0, 128 - suffix.length)}${suffix}`
    if (!used.has(candidate)) return candidate
  }
  return first
}

function seedGuideDefaults() {
  if (!visibleResourceGroups.value.some((group) => group.id === guide.resourceGroupID)) {
    guide.resourceGroupID = pickDefaultResourceGroupID(visibleResourceGroups.value)
  }
  const agentIDs = new Set(sortedAgents.value.map((agent) => agent.id))
  if (guide.target && !agentIDs.has(guide.target)) guide.target = ''
  if (guide.target) return
  const instanceTargets = (selectedInstance.value?.targets || []).filter((target) => agentIDs.has(target))
  if (instanceTargets.length === 1) {
    guide.target = instanceTargets[0]
    return
  }
  if (sortedAgents.value.length === 1) guide.target = sortedAgents.value[0].id
}

async function load({ background = false } = {}) {
  if (refreshInFlight) return
  refreshInFlight = true
  if (!background) {
    loading.value = true
    error.value = ''
  }
  try {
    if (!actor.value) await refreshActor()
    const pluginID = String(route.params.id || '')
    const [nextDetail, nextOperations, nextAgents, nextGroups] = await Promise.all([
      fetchPluginDetail(pluginID),
      fetchPluginOperations(pluginID),
      fetchAgents(),
      fetchResourceGroups().catch(() => [])
    ])
    const visibleDetail = filterPluginDetailForActor(nextDetail, actor.value)
    if (!visibleDetail) throw new Error('当前身份无权查看此插件')
    const previousInstanceID = selectedInstanceID.value
    detail.value = visibleDetail
    operations.value = nextOperations
    agents.value = (Array.isArray(nextAgents) ? nextAgents : []).filter((agent) => String(agent?.id || '').trim())
    resourceGroups.value = Array.isArray(nextGroups) ? nextGroups : []
    selectedInstanceID.value = visibleDetail.instances.some((instance) => instance.id === previousInstanceID)
      ? previousInstanceID
      : visibleDetail.instances[0]?.id || ''
    seedGuideDefaults()
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '读取插件详情失败')
  } finally {
    refreshInFlight = false
    if (!background) loading.value = false
  }
}

async function handleDeployed(instanceID) {
  await load()
  selectedInstanceID.value = instanceID
  deployModalOpen.value = false
}

function startEditEntry(entry) {
  if (!canWrite.value) return
  const parsed = parseFrontendURL(entry.frontend_url)
  guide.domain = parsed.domain
  guide.https = parsed.https
  guide.editingRuleID = entry.rule_id
  guide.target = entry.agent_id
  guide.extraDomainOpen = false
  guideError.value = ''
}

function startExtraDomain() {
  if (!canWrite.value) return
  guide.editingRuleID = 0
  guide.extraDomainOpen = true
  guide.domain = ''
  guide.https = true
  guideError.value = ''
  seedGuideDefaults()
}

function closeTaskGuide() {
  guide.editingRuleID = 0
  guide.extraDomainOpen = false
}

async function submitGuide(payload = { config: {}, secret_replacements: {} }) {
  if (!canSubmitGuide.value || guideBusy.value || busy.value) return
  if (guideBlocker.value) {
    guideError.value = guideBlocker.value
    return
  }
  const pluginID = detail.value.plugin.plugin_id
  const target = String(guide.target || '').trim()
  guideBusy.value = true
  busy.value = hasHTTPBackend.value && taskState.value !== 'undeployed' ? 'publish' : 'deploy'
  guideError.value = ''
  error.value = ''
  try {
    if (hasHTTPBackend.value && (taskState.value === 'undeployed' || showDomainFields.value)) {
      const instance = selectedInstance.value
      const request = {
        instance_id: instance?.id || defaultInstanceID(),
        resource_group_id: instance?.resource_group_id || guide.resourceGroupID,
        targets: [target],
        policy_chains: instance?.policy_chains || [],
        frontend_url: buildFrontendURL(),
        config: stripReadOnlyConfigValues(detail.value.package?.config_schema, taskState.value === 'undeployed' ? payload.config : (instance?.config || {})),
        secret_replacements: taskState.value === 'undeployed' ? (payload.secret_replacements || {}) : {}
      }
      if (guide.editingRuleID > 0) request.rule_id = guide.editingRuleID
      await publishPlugin(pluginID, request)
      closeTaskGuide()
    } else {
      const instanceID = defaultInstanceID()
      await configurePlugin(pluginID, {
        instance_id: instanceID,
        resource_group_id: guide.resourceGroupID,
        targets: [target],
        policy_chains: [],
        bindings: [],
        config: stripReadOnlyConfigValues(detail.value.package?.config_schema, payload.config),
        secret_replacements: payload.secret_replacements || {}
      })
      if (detail.value.plugin.desired_lifecycle !== 'enabled' && detail.value.plugin.current_lifecycle !== 'active') {
        await enablePlugin(pluginID)
      }
      closeTaskGuide()
    }
    await load()
  } catch (cause) {
    const fallback = hasHTTPBackend.value && taskState.value !== 'undeployed' ? '发布插件入口失败' : '部署插件实例失败'
    guideError.value = sanitizePluginText(cause?.message || fallback)
    error.value = guideError.value
  } finally {
    guideBusy.value = false
    busy.value = ''
  }
}

async function lifecycle(action) {
  if (!admin.value || busy.value) return
  busy.value = action
  error.value = ''
  try {
    const pluginID = detail.value.plugin.plugin_id
    if (action === 'enable') await enablePlugin(pluginID)
    else if (action === 'disable') await disablePlugin(pluginID)
    else if (action === 'rollback') await rollbackPlugin(pluginID, detail.value.package.permissions || [])
    await load()
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || `插件 ${action} 失败`)
  } finally {
    busy.value = ''
  }
}

function handleLifecycleAction(action) {
  if (!admin.value || busy.value) return
  if (action === 'enable') {
    lifecycle('enable')
    return
  }
  confirmDialog.value = { visible: true, loading: false, action }
}

function cancelConfirm() {
  confirmDialog.value = { visible: false, loading: false, action: '' }
}

async function confirmAction() {
  const action = confirmDialog.value.action
  if (action === 'delete-instance' ? !canWrite.value : !admin.value) return
  confirmDialog.value.loading = true
  error.value = ''
  try {
    if (action === 'uninstall') {
      await uninstallPlugin(detail.value.plugin.plugin_id)
      await router.push('/plugins')
    } else if (action === 'delete-instance') {
      await deletePluginInstance(detail.value.plugin.plugin_id, selectedInstance.value.id)
      await load()
    } else {
      await lifecycle(action)
    }
  } catch (cause) {
    if (action === 'uninstall' || action === 'delete-instance') {
      const fallback = action === 'delete-instance' ? '删除插件实例失败' : '卸载插件失败'
      error.value = sanitizePluginText(cause?.message || fallback)
    }
  } finally {
    confirmDialog.value = { visible: false, loading: false, action: '' }
  }
}

async function handleConfigSaved() {
  await load()
  configModalOpen.value = false
}

async function handleConfigRefreshed() {
  await load()
}

function exportSafeDiagnostics() {
  const payload = safePluginExport(detail.value, operations.value)
  const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `plugin-${detail.value.plugin.plugin_id}-redacted.json`
  anchor.click()
  URL.revokeObjectURL(url)
}

async function retryAgent(status) {
  if (!admin.value || retryingAgent.value) return
  retryingAgent.value = status.agent_id
  error.value = ''
  try {
    const revision = Math.max(Number(status.desired_revision) || 0, Number(status.target_revision) || 0)
    if (!revision) throw new Error('Agent revision 无效')
    await retryRevision({ agent_id: status.agent_id, desired_revision: revision, agents: [] }, { agent_id: status.agent_id, desired_revision: revision })
    await load()
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '重试 Agent revision 失败')
  } finally {
    retryingAgent.value = ''
  }
}
</script>

<template>
  <main class="plugin-detail-page">
    <div v-if="loading" class="plugin-detail-page__loading">
      <div class="spinner"></div>
      <p>正在读取插件详情…</p>
    </div>

    <div v-else-if="error && !detail" role="alert">
      <EmptyState title="读取失败" :description="error" />
    </div>

    <template v-else-if="detail">
      <header class="page-header">
        <div class="page-header__left">
          <RouterLink to="/plugins" class="back-link">← 已安装插件</RouterLink>
          <h1 class="page-title">{{ detail.package.manifest?.name || detail.plugin.plugin_id }}</h1>
          <p class="page-subtitle">{{ sourceLabel }} · {{ lifecycleLabel }} · {{ deploymentStatusLabel }}</p>
        </div>
        <div v-if="showHeaderOps" class="page-header__right plugin-detail-actions">
          <button class="btn btn-secondary" type="button" @click="exportSafeDiagnostics">导出脱敏诊断</button>
          <template v-if="admin">
            <button class="btn btn-primary" type="button" :disabled="!!busy" @click="handleLifecycleAction('enable')">启用</button>
            <button class="btn btn-secondary" type="button" :disabled="!!busy" @click="handleLifecycleAction('disable')">停用</button>
            <button class="btn btn-secondary" type="button" :disabled="!!busy || !detail.plugin.rollback_package_digest" @click="handleLifecycleAction('rollback')">回滚</button>
            <button class="btn btn-danger" type="button" :disabled="!!busy || detail.plugin.current_lifecycle !== 'disabled'" @click="handleLifecycleAction('uninstall')">卸载</button>
          </template>
        </div>
      </header>

      <p v-if="error" class="plugin-alert" role="alert">{{ error }}</p>

      <section class="plugin-task" data-test="plugin-task-center" aria-label="插件任务">
        <p class="plugin-task__purpose">{{ pluginPurpose }}</p>
        <p class="plugin-task__status" data-test="plugin-task-status">{{ taskStateLabel }}</p>
        <p v-if="taskHint" class="plugin-task__hint">{{ taskHint }}</p>
        <p v-if="!canSubmitGuide && (taskState === 'undeployed' || taskState === 'unpublished')" class="plugin-task__hint">
          当前身份可以看懂下一步，但不能提交部署或发布。
        </p>

        <div v-if="publishedEntries.length" class="plugin-published" data-test="plugin-published-entries">
          <article
            v-for="entry in publishedEntries"
            :key="entry.rule_id"
            class="plugin-published__item"
            data-test="plugin-published-entry"
          >
            <strong>{{ entry.frontend_url }}</strong>
            <span>{{ entry.enabled ? '已启用' : '未启用' }}</span>
            <span>{{ entry.accessible ? '可访问' : '还不能访问' }}</span>
            <span>节点 {{ entry.agent_id }}</span>
            <button
              v-if="canWrite"
              class="btn btn-secondary btn-sm"
              type="button"
              :disabled="!!busy || guideBusy"
              @click="startEditEntry(entry)"
            >
              修改入口
            </button>
          </article>
          <button
            v-if="canWrite && hasHTTPBackend"
            class="btn btn-secondary btn-sm"
            type="button"
            :disabled="!!busy || guideBusy"
            @click="startExtraDomain"
          >
            再发布一条域名
          </button>
        </div>

        <form v-if="showGuideForm" class="plugin-guide" data-test="plugin-task-guide" @submit.prevent>
          <div v-if="showResourceGroup" class="plugin-guide__fields">
            <label>
              <span>资源组</span>
              <select v-model="guide.resourceGroupID" data-test="plugin-guide-resource-group" :disabled="!canSubmitGuide || !visibleResourceGroups.length">
                <option v-if="!visibleResourceGroups.length" value="">暂无可见资源组</option>
                <option v-for="group in visibleResourceGroups" :key="group.id" :value="group.id">{{ resourceGroupDisplayName(group) }}</option>
              </select>
            </label>
          </div>

          <fieldset v-if="showNodeFields" class="plugin-guide__agents">
            <legend>部署节点</legend>
            <div v-if="sortedAgents.length" class="plugin-guide__agent-grid">
              <label
                v-for="agent in sortedAgents"
                :key="agent.id"
                class="plugin-guide__agent"
                :class="{ 'plugin-guide__agent--selected': guide.target === agent.id }"
              >
                <input
                  v-model="guide.target"
                  type="radio"
                  name="plugin-guide-target"
                  :value="agent.id"
                  data-test="plugin-guide-target"
                  :disabled="!canSubmitGuide || guide.editingRuleID > 0"
                >
                <span>
                  <strong>{{ agent.name || agent.id }}</strong>
                  <small>{{ getAgentStatusLabel(getAgentStatus(agent)) }}</small>
                </span>
              </label>
            </div>
            <p v-else class="plugin-config-empty">当前没有可选择的节点。</p>
          </fieldset>

          <div v-if="showDomainFields" class="plugin-guide__fields">
            <label>
              <span>入口域名</span>
              <input
                v-model="guide.domain"
                type="text"
                data-test="plugin-guide-domain"
                placeholder="media.example.com"
                autocomplete="off"
                :disabled="!canSubmitGuide"
              >
            </label>
            <label class="plugin-guide__https">
              <input v-model="guide.https" type="checkbox" data-test="plugin-guide-https" :disabled="!canSubmitGuide">
              <span>使用 HTTPS</span>
            </label>
          </div>

          <p v-if="guideError || guideBlocker" class="plugin-alert" role="alert">{{ guideError || guideBlocker }}</p>

          <PluginDeclarativeUI
            v-if="showGuideConfig"
            :document="guideDocument"
            :config="{}"
            :secret-fields="[]"
            :saving="guideSubmitDisabled"
            :can-configure="canSubmitGuide"
            :can-act="false"
            @submit="submitGuide"
          />
          <div v-else class="plugin-guide__actions">
            <button
              class="btn btn-primary"
              type="button"
              data-test="plugin-task-primary"
              :disabled="guideSubmitDisabled"
              @click="submitGuide({ config: {}, secret_replacements: {} })"
            >
              {{ guideSubmitLabel }}
            </button>
          </div>
        </form>
      </section>

      <section class="plugin-section">
        <div class="plugin-section-heading">
          <div>
            <h2>实例</h2>
            <p>查看实例状态；部署与配置编辑在弹窗中完成。</p>
          </div>
          <button v-if="admin && hasInstances" class="btn btn-secondary" type="button" :disabled="!!busy" @click="deployModalOpen = true">
            部署
          </button>
        </div>

        <BaseTabs
          v-if="instanceTabs.length"
          :tabs="instanceTabs"
          :model-value="selectedInstanceID"
          @update:model-value="selectedInstanceID = $event"
        />
        <div v-if="selectedInstance" class="instance-facts">
          <span>目标：{{ selectedInstance.targets.join(', ') || '—' }}</span>
          <span>版本：{{ selectedInstance.config_version }}</span>
          <span>状态：{{ selectedInstance.current_state }}</span>
          <div v-if="canWrite" class="instance-actions">
            <button v-if="!formEmpty" class="btn btn-secondary btn-sm" type="button" @click="configModalOpen = true">编辑配置</button>
            <button class="btn btn-danger btn-sm" type="button" :disabled="!!busy" @click="confirmDialog = { visible: true, loading: false, action: 'delete-instance' }">删除实例</button>
          </div>
        </div>
        <p v-else class="plugin-config-empty">尚未部署。</p>
        <p v-if="selectedInstance && canWrite && formEmpty" class="plugin-config-empty">此插件没有宿主允许的可配置字段。</p>
        <p v-if="selectedInstance && !canWrite" class="plugin-config-empty">当前身份只有只读权限。</p>
      </section>

      <details v-if="!showHeaderOps" class="plugin-ops">
        <summary>生命周期与诊断</summary>
        <div class="plugin-detail-actions">
          <button class="btn btn-secondary" type="button" @click="exportSafeDiagnostics">导出脱敏诊断</button>
          <template v-if="admin">
            <button class="btn btn-secondary" type="button" :disabled="!!busy" @click="handleLifecycleAction('enable')">启用</button>
            <button class="btn btn-secondary" type="button" :disabled="!!busy" @click="handleLifecycleAction('disable')">停用</button>
            <button class="btn btn-secondary" type="button" :disabled="!!busy || !detail.plugin.rollback_package_digest" @click="handleLifecycleAction('rollback')">回滚</button>
            <button class="btn btn-danger" type="button" :disabled="!!busy || detail.plugin.current_lifecycle !== 'disabled'" @click="handleLifecycleAction('uninstall')">卸载</button>
          </template>
        </div>
      </details>

      <details class="plugin-technical">
        <summary>技术详情</summary>
        <PluginPackageSummary :detail="detail.package" :source="source" />
        <PluginRiskNotices :package-detail="detail.package" :source="source" />
      </details>

      <section class="plugin-section"><h2>逐 Agent 状态、预算与故障</h2><PluginAgentStatusTable :statuses="detail.agent_statuses" :actionable="admin" :busy-agent="retryingAgent" @retry="retryAgent" /></section>
      <section v-if="selectedInstance" class="plugin-section"><h2>实例 / Agent 运行日志</h2><PluginLogViewer :plugin-id="detail.plugin.plugin_id" :instance-id="selectedInstance.id" :agents="selectedInstance.targets || []" /></section>
      <section class="plugin-section"><h2>生命周期操作与审计</h2><PluginOperationTimeline :operations="operations" /></section>

      <PluginDeployModal
        v-if="admin && hasInstances"
        v-model="deployModalOpen"
        :plugin-id="detail.plugin.plugin_id"
        :instances="detail.instances"
        :agents="agents"
        :resource-groups="visibleResourceGroups"
        :config-schema="detail.package?.config_schema || null"
        :document="deploymentDocument"
        :form-empty="formEmpty"
        :desired-lifecycle="detail.plugin.desired_lifecycle"
        :current-lifecycle="detail.plugin.current_lifecycle"
        @deployed="handleDeployed"
      />

      <PluginInstanceConfigModal
        v-model="configModalOpen"
        :plugin-id="detail.plugin.plugin_id"
        :instance="selectedInstance"
        :document="uiDocument"
        :config="formConfig"
        :secret-fields="selectedSecretFields"
        :config-schema="detail.package?.config_schema || null"
        :package-detail="detail.package"
        :can-write="canWrite"
        @saved="handleConfigSaved"
        @refreshed="handleConfigRefreshed"
      />

      <DeleteConfirmDialog
        :show="confirmDialog.visible"
        :title="confirmCopy.title"
        :message="confirmCopy.message"
        :name="confirmName"
        :confirm-text="confirmCopy.confirmText"
        :loading="confirmDialog.loading"
        @confirm="confirmAction"
        @cancel="cancelConfirm"
      />
    </template>
  </main>
</template>

<style scoped>
.plugin-detail-page { max-width: 1180px; display: grid; gap: var(--space-6); margin: 0 auto; }

.plugin-detail-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}
.back-link:hover { color: var(--color-primary); }

.plugin-detail-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }

.plugin-alert { color: var(--color-danger); }
.plugin-technical,
.plugin-ops { display: grid; gap: var(--space-4); }
.plugin-technical summary,
.plugin-ops summary { cursor: pointer; color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-config-empty { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }

.plugin-task {
  display: grid;
  gap: var(--space-4);
  padding: var(--space-5);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}
.plugin-task__purpose,
.plugin-task__hint { margin: 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.plugin-task__status { margin: 0; font-size: var(--text-lg); font-weight: 600; }
.plugin-task__actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }

.plugin-guide,
.plugin-guide__fields,
.plugin-published { display: grid; gap: var(--space-4); }
.plugin-guide__fields label { display: grid; gap: var(--space-2); color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-guide select,
.plugin-guide input[type="text"] {
  min-width: 0;
  padding: .6rem .75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font: inherit;
}
.plugin-guide select:focus-visible,
.plugin-guide input[type="text"]:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 18%, transparent);
}
.plugin-guide__https {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.plugin-guide__agents { display: grid; gap: var(--space-3); min-width: 0; margin: 0; padding: 0; border: 0; }
.plugin-guide__agents legend { margin-bottom: var(--space-2); color: var(--color-text-primary); font-weight: 600; font-size: var(--text-sm); }
.plugin-guide__agent-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: var(--space-3); }
.plugin-guide__agent {
  display: flex;
  align-items: start;
  gap: var(--space-3);
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  cursor: pointer;
}
.plugin-guide__agent--selected { border-color: var(--color-primary); background: color-mix(in srgb, var(--color-primary) 6%, var(--color-bg-surface)); }
.plugin-guide__agent input { margin-top: .2rem; accent-color: var(--color-primary); }
.plugin-guide__agent span { min-width: 0; display: grid; gap: 2px; }
.plugin-guide__agent strong,
.plugin-guide__agent small { overflow-wrap: anywhere; }
.plugin-guide__agent small { color: var(--color-text-muted); }
.plugin-guide__actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
.plugin-published__item {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.plugin-published__item strong { color: var(--color-text-primary); overflow-wrap: anywhere; }

.plugin-section { display: grid; gap: var(--space-4); padding-top: var(--space-5); border-top: 1px solid var(--color-border-subtle); }
.plugin-section h2 { margin: 0; font-size: var(--text-lg); }
.plugin-section-heading { display: flex; align-items: start; justify-content: space-between; gap: var(--space-4); }
.plugin-section-heading > div { display: grid; gap: var(--space-1); }
.plugin-section-heading p { margin: 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.instance-facts { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); color: var(--color-text-muted); font-size: var(--text-sm); }
.instance-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-left: auto; }

@media (max-width: 640px) {
  .plugin-section-heading { align-items: stretch; flex-direction: column; }
  .plugin-section-heading .btn { width: 100%; }
  .instance-actions { margin-left: 0; }
}
</style>
