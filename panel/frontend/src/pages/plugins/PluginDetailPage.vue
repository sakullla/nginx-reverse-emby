<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { fetchAgents, fetchHttpRulesPage } from '../../api'
import { fetchResourceGroups } from '../../api/access'
import {
  deletePluginInstance,
  disablePlugin,
  enablePlugin,
  fetchPluginDetail,
  fetchPluginOperations,
  rollbackPlugin,
  uninstallPlugin,
  unpublishPlugin
} from '../../api/plugins'
import { safePluginExport, sanitizePluginText, schemaToUIComponents, stripWriteOnlyConfigValues } from '../../api/pluginSecurity'
import { retryRevision } from '../../api/operations'
import { filterPluginDetailForActor, useAccessControl, visibleResourceGroupsForActor } from '../../context/useAccessControl'
import { messageStore } from '../../stores/messages'
import BaseBadge from '../../components/base/BaseBadge.vue'
import BaseTabs from '../../components/base/BaseTabs.vue'
import DeleteConfirmDialog from '../../components/DeleteConfirmDialog.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import PluginAgentStatusTable from '../../components/plugins/PluginAgentStatusTable.vue'
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
const httpRules = ref([])
const selectedInstanceID = ref('')
const selectedFaceID = ref('')
const retryingAgent = ref('')
const deployModalOpen = ref(false)
const configModalOpen = ref(false)
const deployIntent = ref('deploy')
const deployPublishedEntry = ref(null)

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
const declaredFaces = computed(() => Array.isArray(detail.value?.faces) ? detail.value.faces : [])
const hasDeclaredFaceProjection = computed(() => declaredFaces.value.length > 0)
const hasLocalManagementFace = computed(() => declaredFaces.value.some((face) => face?.face_id === 'local-management'))
const hasAgentExecutionFace = computed(() => declaredFaces.value.some((face) => face?.face_id === 'agent-execution'))
const showAgentExecutionStatus = computed(() => hasAgentExecutionFace.value || !hasDeclaredFaceProjection.value)
const targetEligibility = computed(() => detail.value?.target_eligibility || null)
const usesCanonicalLocalTarget = computed(() => targetEligibility.value?.agent_targets_allowed === false)
const canonicalLocalTargetID = computed(() => String(targetEligibility.value?.canonical_local_target_id || '').trim())
const refreshIntervalMs = 5000
let refreshTimer = 0
let refreshInFlight = false

const instanceTabs = computed(() => (detail.value?.instances || []).map((instance) => ({
  id: instance.id,
  label: `${instance.id} · ${instance.resource_group_id}`
})))
const faceTabs = computed(() => declaredFaces.value.map((face) => ({
  id: face.face_id,
  label: face.face_id === 'local-management' ? '本地管理面' : face.face_id === 'agent-execution' ? 'Agent 执行面' : face.face_id
})))

const httpRuleOptions = computed(() => {
  const options = []
  const seen = new Set()
  for (const rule of httpRules.value) {
    const value = String(rule?.frontend_url || rule?.frontend || '').trim()
    if (!value || seen.has(value)) continue
    seen.add(value)
    options.push({ value, label: String(rule?.name || value) })
  }
  return options
})

const isDeclarativeUI = computed(() => !!detail.value?.package?.declarative_ui)
const uiDocument = computed(() => {
  const pkg = detail.value?.package
  if (!pkg) return null
  if (pkg.declarative_ui) return bindHttpRuleOptions(pkg.declarative_ui)
  const components = bindHttpRuleOptions({ components: schemaToUIComponents(pkg.config_schema) }).components
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

const confirmDialog = ref({ visible: false, loading: false, action: '', entry: null })
const pluginName = computed(() => detail.value?.package?.manifest?.name || detail.value?.plugin?.plugin_id || '')
const confirmName = computed(() => {
  if (confirmDialog.value.action === 'delete-instance') return selectedInstance.value?.id || ''
  if (confirmDialog.value.action === 'delete-entry') return sanitizePluginText(confirmDialog.value.entry?.frontend_url || '')
  return pluginName.value
})
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
const isUpgrading = computed(() => {
  const plugin = detail.value?.plugin
  if (!plugin) return false
  if (String(plugin.current_lifecycle || '').trim() === 'upgrading') return true
  return String(plugin.pending_kind || '').trim() === 'upgrade' && Boolean(String(plugin.pending_operation_id || '').trim())
})
const taskState = computed(() => {
  if (isUpgrading.value) return 'upgrading'
  if (!(detail.value?.instances || []).length) return 'undeployed'
  if (!hasHTTPBackend.value) return 'deployed'
  if (!publishedEntries.value.length) return 'unpublished'
  if (!publishedEntries.value.some((entry) => entry.enabled && entry.accessible)) return 'published-unavailable'
  return 'available'
})
const taskStateLabel = computed(() => ({
  upgrading: '正在升级',
  undeployed: '还没部署',
  unpublished: '还没发布域名',
  'published-unavailable': '已发布但还不能访问',
  available: '已可用',
  deployed: '已部署'
}[taskState.value] || ''))
const pluginPurpose = computed(() => {
  const description = String(detail.value?.package?.manifest?.description || '').trim()
  if (description) return description
  return hasHTTPBackend.value
    ? '把插件部署到所选节点，再填写一条入口域名即可发布。'
    : '把插件部署到所选节点后即可在这些节点上使用。'
})
const taskHint = computed(() => {
  switch (taskState.value) {
    case 'upgrading':
      return '新版本正在应用到节点。完成前请等待，不要重复提交升级。'
    case 'undeployed':
      return '下一步：选择节点开始部署。'
    case 'unpublished':
      return '还差发布：填写一条入口域名。'
    case 'published-unavailable':
      return '入口已发布，但现在还不能访问。可在「更多」里查看节点状态并重试。'
    case 'deployed':
      return '插件已部署到所选节点。'
    default:
      return ''
  }
})
const primaryTaskLabel = computed(() => (taskState.value === 'unpublished' ? '发布到域名' : '开始部署'))
const showPrimaryTask = computed(() => taskState.value === 'undeployed' || taskState.value === 'unpublished')
const deployModalInstance = computed(() => (deployIntent.value === 'deploy' ? null : selectedInstance.value))

const hasPendingOperation = computed(() => Boolean(String(detail.value?.plugin?.pending_operation_id || '').trim()))
const hasInstances = computed(() => (detail.value?.instances || []).length > 0)
const ownsSingletonControlPlaneSurface = computed(() => {
  const points = detail.value?.package?.manifest?.extension_points
  return Array.isArray(points) && (points.includes('ui.route') || points.includes('resource.group'))
})
const canCreateInstance = computed(() => !ownsSingletonControlPlaneSurface.value || !hasInstances.value)
const showUninstallOnTask = computed(() => admin.value && !hasInstances.value)
const uninstallNeedsDisable = computed(() => hasInstances.value && detail.value?.plugin?.current_lifecycle !== 'disabled' && detail.value?.plugin?.desired_lifecycle !== 'disabled')

const confirmCopy = computed(() => {
  switch (confirmDialog.value.action) {
    case 'disable':
      return { title: '确认停用插件', message: '停用后该插件将停止处理流量，依赖其防护的流量可能中断；可随时重新启用。', confirmText: '确认停用' }
    case 'rollback':
      return { title: '确认回滚插件', message: '回滚将把插件恢复到上一版本，并可能变更其权限。', confirmText: '确认回滚' }
    case 'delete-instance':
      return { title: '确认删除部署实例', message: '该实例会从所有目标 Agent 下线，配置、插件密钥及其发布的入口规则将一并清理。', confirmText: '确认删除实例' }
    case 'delete-entry':
      return { title: '确认删除入口', message: '删除后该域名将不再指向此插件，已发布的访问会立即失效。插件实例仍保留，需要时可以再发布。', confirmText: '确认删除入口' }
    case 'uninstall':
      return {
        title: '确认卸载插件',
        message: uninstallNeedsDisable.value
          ? '插件还在运行。确认后会先停用，再卸载并移除配置。此操作不可撤销。'
          : hasPendingOperation.value
            ? '这个插件还有未完成的操作。确认后会继续卸载；若仍被占用，等当前操作结束后再试。'
            : '卸载将移除插件及其配置，此操作不可撤销。',
        confirmText: uninstallNeedsDisable.value ? '停用并卸载' : '确认卸载'
      }
    default:
      return { title: '确认卸载插件', message: '卸载将移除插件及其配置，此操作不可撤销。', confirmText: '确认卸载' }
  }
})

onMounted(() => {
  void load()
  refreshTimer = window.setInterval(() => {
    if (busy.value || configModalOpen.value || deployModalOpen.value || confirmDialog.value.visible) return
    void load({ background: true })
  }, refreshIntervalMs)
})

onBeforeUnmount(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
})

function bindHttpRuleOptions(document) {
  if (!document || typeof document !== 'object') return document
  const options = httpRuleOptions.value
  const walk = (components) => (Array.isArray(components) ? components : []).map((component) => {
    if (!component || typeof component !== 'object') return component
    const next = { ...component }
    if (Array.isArray(component.children)) next.children = walk(component.children)
    if (component.options_source === 'http_rule') {
      next.options = options
      if (!options.length) next.description = '当前没有可见的 HTTP 规则'
    }
    return next
  })
  if (Array.isArray(document)) return walk(document)
  return { ...document, components: walk(document.components) }
}

async function refreshHttpRules() {
  if (typeof fetchHttpRulesPage !== 'function') {
    httpRules.value = []
    return
  }
  try {
    const page = await fetchHttpRulesPage({ page: 1, page_size: 200 })
    httpRules.value = Array.isArray(page?.items) ? page.items : []
  } catch {
    httpRules.value = []
  }
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
    await refreshHttpRules()
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
    const faceIDs = visibleDetail.faces?.map((face) => face.face_id) || []
    selectedFaceID.value = faceIDs.includes(selectedFaceID.value)
      ? selectedFaceID.value
      : (faceIDs.includes('local-management') ? 'local-management' : faceIDs[0] || '')
  } catch (cause) {
    error.value = humanLoadError(cause, '读取插件详情失败')
    if (detail.value) messageStore.error(error.value)
  } finally {
    refreshInFlight = false
    if (!background) loading.value = false
  }
}

async function handleDeployed(instanceID) {
  await load()
  selectedInstanceID.value = instanceID
  deployModalOpen.value = false
  deployPublishedEntry.value = null
}

async function openDeployModal(intent = 'deploy', entry = null) {
  deployIntent.value = intent
  deployPublishedEntry.value = entry
  deployModalOpen.value = true
  await refreshHttpRules()
}

async function openConfigModal() {
  configModalOpen.value = true
  await refreshHttpRules()
}

function startPrimaryTask() {
  if (taskState.value === 'unpublished') {
    void openDeployModal('publish')
    return
  }
  void openDeployModal('deploy')
}

function startEditEntry(entry) {
  if (!admin.value) return
  void openDeployModal('update', entry)
}

function startDeleteEntry(entry) {
  if (!admin.value || !entry) return
  confirmDialog.value = { visible: true, loading: false, action: 'delete-entry', entry }
}

function startExtraDomain() {
  if (!admin.value) return
  void openDeployModal('publish')
}

function publishedEntryHref(entry) {
  const url = String(entry?.frontend_url || '').trim()
  return /^https?:\/\//i.test(url) ? url : ''
}

function agentLabel(agentID) {
  const id = String(agentID || '').trim()
  const agent = agents.value.find((item) => String(item?.id || '') === id)
  const name = String(agent?.name || '').trim()
  return name || id
}

function instanceTargetLabels(instance) {
  if (usesCanonicalLocalTarget.value) return canonicalLocalTargetID.value ? `本地（${canonicalLocalTargetID.value}）` : '本地'
  const ids = Array.isArray(instance?.targets) ? instance.targets : []
  if (!ids.length) return '—'
  return ids.map((id) => agentLabel(id)).join('、')
}

function faceHostScope(faceID) {
  const face = declaredFaces.value.find((item) => item?.face_id === faceID)
  return sanitizePluginText(face?.host_scope || '')
}

function faceStateTone(state) {
  const value = String(state || '').trim().toLowerCase()
  if (['failed', 'degraded', 'crashed'].includes(value)) return 'danger'
  if (['active', 'ready', 'applied', 'enabled'].includes(value)) return 'success'
  return 'warning'
}

function localManagementState(instance) {
  return instance?.current_state || '尚未部署'
}

function agentExecutionTargetLabels(instance) {
  if (!instance) return '尚未部署'
  const labels = instanceTargetLabels(instance)
  return labels === '—' ? '尚未选择 Agent' : labels
}

const lifecycleSuccess = {
  enable: '插件已启用',
  disable: '插件已停用',
  rollback: '插件已回滚'
}

async function lifecycle(action) {
  if (!admin.value || busy.value) return
  busy.value = action
  try {
    const pluginID = detail.value.plugin.plugin_id
    if (action === 'enable') await enablePlugin(pluginID)
    else if (action === 'disable') await disablePlugin(pluginID)
    else if (action === 'rollback') await rollbackPlugin(pluginID, detail.value.package.permissions || [])
    await load()
    messageStore.success(lifecycleSuccess[action] || '插件操作已完成')
  } catch (cause) {
    messageStore.error(humanPluginError(cause, `插件 ${action} 失败`))
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
  confirmDialog.value = { visible: false, loading: false, action: '', entry: null }
}

function humanPluginError(cause, fallback) {
  const raw = sanitizePluginText(cause?.message || fallback || '').trim()
  if (/control-plane application already has an instance/i.test(raw)) {
    return '这个控制面应用已经有一个实例，请编辑现有实例。'
  }
  if (/already pending|plugin state conflict/i.test(raw)) {
    return '这个插件还有未完成的操作，所以这次没有提交。等当前停用/升级结束后再试。'
  }
  if (/must be disabled and drained|runtime_not_drained/i.test(raw)) {
    return '请先停用插件，等节点完成停用后再卸载。'
  }
  if (/still used by .* bound rule/i.test(raw)) {
    return '还有 HTTP 规则绑定这个实例。先改入口或删掉绑定规则，再删除实例。'
  }
  return raw || fallback || '操作失败'
}

async function uninstallPluginWithPrep() {
  const pluginID = detail.value.plugin.plugin_id
  if (uninstallNeedsDisable.value) {
    await disablePlugin(pluginID)
    await load()
  }
  await uninstallPlugin(pluginID)
}

async function confirmAction() {
  const action = confirmDialog.value.action
  if (action === 'delete-instance' ? !canWrite.value : !admin.value) return
  confirmDialog.value.loading = true
  try {
    if (action === 'uninstall') {
      await uninstallPluginWithPrep()
      confirmDialog.value = { visible: false, loading: false, action: '', entry: null }
      messageStore.success('插件已卸载')
      await router.push('/plugins')
    } else if (action === 'delete-instance') {
      await deletePluginInstance(detail.value.plugin.plugin_id, selectedInstance.value.id)
      confirmDialog.value = { visible: false, loading: false, action: '', entry: null }
      await load()
      messageStore.success('插件实例已删除')
    } else if (action === 'delete-entry') {
      const entry = confirmDialog.value.entry
      const ruleID = Number(entry?.rule_id)
      const agentID = String(entry?.agent_id || '').trim()
      if (!Number.isInteger(ruleID) || ruleID <= 0 || !agentID) throw new Error('入口规则无效')
      await unpublishPlugin(detail.value.plugin.plugin_id, { rule_id: ruleID, targets: [agentID] })
      confirmDialog.value = { visible: false, loading: false, action: '', entry: null }
      configModalOpen.value = false
      await load()
      messageStore.success('入口已删除')
    } else {
      await lifecycle(action)
      confirmDialog.value = { visible: false, loading: false, action: '', entry: null }
    }
  } catch (cause) {
    const fallback = action === 'delete-instance'
      ? '删除插件实例失败'
      : action === 'delete-entry'
        ? '删除入口失败'
        : action === 'uninstall' ? '卸载插件失败' : `插件 ${action} 失败`
    messageStore.error(humanPluginError(cause, fallback))
    confirmDialog.value = { ...confirmDialog.value, loading: false }
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

function humanLoadError(cause, fallback) {
  const raw = sanitizePluginText(cause?.message || fallback || '').trim()
  if (/status code 5\d\d|network error|failed to fetch|502/i.test(raw)) {
    return '暂时连不上服务，请稍后重试。'
  }
  return raw || fallback || '读取失败'
}

async function retryAgent(status) {
  if (!admin.value || retryingAgent.value) return
  retryingAgent.value = status.agent_id
  try {
    const revision = Math.max(Number(status.desired_revision) || 0, Number(status.target_revision) || 0)
    if (!revision) throw new Error('Agent revision 无效')
    await retryRevision({ agent_id: status.agent_id, desired_revision: revision, agents: [] }, { agent_id: status.agent_id, desired_revision: revision })
    await load()
    messageStore.success('已提交重试')
  } catch (cause) {
    messageStore.error(sanitizePluginText(cause?.message || '重试 Agent revision 失败'))
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
      <EmptyState title="读取失败" :description="error">
        <template #action>
          <div class="plugin-detail-empty-actions">
            <button class="btn btn-secondary" type="button" @click="load()">重试</button>
            <RouterLink class="btn btn-secondary" to="/plugins">返回已安装插件</RouterLink>
          </div>
        </template>
      </EmptyState>
    </div>

    <template v-else-if="detail">
      <header class="page-header">
        <div class="page-header__left">
          <RouterLink to="/plugins" class="back-link">← 已安装插件</RouterLink>
          <h1 class="page-title">{{ detail.package.manifest?.name || detail.plugin.plugin_id }}</h1>
          <p class="page-subtitle">{{ sourceLabel }} · {{ lifecycleLabel }} · {{ deploymentStatusLabel }}</p>
        </div>
      </header>

      <section class="plugin-task" data-test="plugin-task-center" aria-label="插件任务">
        <p class="plugin-task__purpose">{{ pluginPurpose }}</p>
        <p class="plugin-task__status" data-test="plugin-task-status">{{ taskStateLabel }}</p>
        <p v-if="taskHint" class="plugin-task__hint">{{ taskHint }}</p>
        <p v-if="!admin && showPrimaryTask" class="plugin-task__hint">
          当前身份可以看懂下一步，但不能提交部署或发布。
        </p>

        <div v-if="publishedEntries.length" class="plugin-http-entries" data-test="plugin-published-entries">
          <header class="plugin-http-entries__head">
            <div>
              <h2>HTTP 入口</h2>
              <p>已发布的访问域名。点域名可直接打开，状态一眼可扫。</p>
            </div>
            <span class="plugin-http-entries__count">{{ publishedEntries.length }} 条</span>
          </header>
          <ul class="plugin-http-entries__list">
            <li
              v-for="entry in publishedEntries"
              :key="`${entry.agent_id}:${entry.rule_id}`"
              class="plugin-http-entry"
              data-test="plugin-published-entry"
            >
              <div class="plugin-http-entry__main">
                <a
                  v-if="publishedEntryHref(entry)"
                  class="plugin-http-entry__url"
                  :href="publishedEntryHref(entry)"
                  target="_blank"
                  rel="noopener noreferrer"
                >{{ sanitizePluginText(entry.frontend_url) }}</a>
                <strong v-else class="plugin-http-entry__url">{{ sanitizePluginText(entry.frontend_url) }}</strong>
                <div class="plugin-http-entry__meta">
                  <BaseBadge :tone="entry.enabled ? 'success' : 'neutral'" size="sm" dot>
                    {{ entry.enabled ? '已启用' : '未启用' }}
                  </BaseBadge>
                  <BaseBadge :tone="entry.accessible ? 'success' : 'warning'" size="sm" dot>
                    {{ entry.accessible ? '可访问' : '还不能访问' }}
                  </BaseBadge>
                  <span class="plugin-http-entry__node">节点 {{ agentLabel(entry.agent_id) }}</span>
                </div>
              </div>
              <div class="plugin-http-entry__actions">
                <button
                  v-if="admin"
                  class="btn btn-secondary btn-sm"
                  type="button"
                  :disabled="!!busy"
                  @click="startEditEntry(entry)"
                >
                  修改入口
                </button>
                <button
                  v-if="admin"
                  class="btn btn-danger btn-sm"
                  type="button"
                  data-test="plugin-delete-entry"
                  :disabled="!!busy"
                  @click="startDeleteEntry(entry)"
                >
                  删除入口
                </button>
              </div>
            </li>
          </ul>
          <button
            v-if="admin && hasHTTPBackend"
            class="plugin-http-entries__add"
            type="button"
            :disabled="!!busy"
            @click="startExtraDomain"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            再发布一条域名
          </button>
        </div>

        <div v-if="showPrimaryTask || showUninstallOnTask" class="plugin-task__actions">
          <button
            v-if="showPrimaryTask"
            class="btn btn-primary"
            type="button"
            data-test="plugin-task-primary"
            :disabled="!!busy || !admin"
            @click="startPrimaryTask"
          >
            {{ primaryTaskLabel }}
          </button>
          <button
            v-if="showUninstallOnTask"
            class="btn btn-danger"
            type="button"
            data-test="plugin-task-uninstall"
            :disabled="!!busy"
            @click="handleLifecycleAction('uninstall')"
          >
            卸载
          </button>
        </div>
      </section>

      <section class="plugin-section">
        <div class="plugin-section-heading">
          <div>
            <h2>实例</h2>
            <p>查看实例状态；部署与配置编辑在弹窗中完成。</p>
          </div>
          <button v-if="admin && canCreateInstance" class="btn btn-secondary" type="button" :disabled="!!busy" @click="openDeployModal('deploy')">
            部署
          </button>
        </div>

        <BaseTabs
          v-if="instanceTabs.length"
          :tabs="instanceTabs"
          :model-value="selectedInstanceID"
          @update:model-value="selectedInstanceID = $event"
        />
        <BaseTabs
          v-if="faceTabs.length > 1"
          :tabs="faceTabs"
          :model-value="selectedFaceID"
          data-test="plugin-face-switch"
          @update:model-value="selectedFaceID = $event"
        />
        <div v-if="hasDeclaredFaceProjection" class="plugin-face-list" aria-label="插件运行面">
          <section
            v-if="hasLocalManagementFace && selectedFaceID === 'local-management'"
            class="plugin-face"
            data-test="plugin-face-local-management"
          >
            <div class="plugin-face__heading">
              <div>
                <strong>本地管理面</strong>
                <small v-if="faceHostScope('local-management')">{{ faceHostScope('local-management') }}</small>
              </div>
              <BaseBadge :tone="faceStateTone(localManagementState(selectedInstance))" size="sm" dot>
                {{ localManagementState(selectedInstance) }}
              </BaseBadge>
            </div>
            <p>目标固定为 {{ canonicalLocalTargetID || '本地' }}；配置与管理状态只归属当前控制面。</p>
          </section>
          <section
            v-if="hasAgentExecutionFace && selectedFaceID === 'agent-execution'"
            class="plugin-face"
            data-test="plugin-face-agent-execution"
          >
            <div class="plugin-face__heading">
              <div>
                <strong>Agent 执行面</strong>
                <small v-if="faceHostScope('agent-execution')">{{ faceHostScope('agent-execution') }}</small>
              </div>
              <BaseBadge tone="neutral" size="sm">{{ (selectedInstance?.targets || []).length }} 个目标</BaseBadge>
            </div>
            <p>Agent：{{ agentExecutionTargetLabels(selectedInstance) }}。同步、离线与执行错误在此运行面单独展示。</p>
          </section>
        </div>
        <div v-if="selectedInstance" class="instance-facts">
          <span>版本：{{ selectedInstance.config_version }}</span>
          <template v-if="!hasDeclaredFaceProjection">
            <span>目标：{{ instanceTargetLabels(selectedInstance) }}</span>
            <span>状态：{{ selectedInstance.current_state }}</span>
          </template>
          <div v-if="canWrite" class="instance-actions">
            <button v-if="!formEmpty" class="btn btn-secondary btn-sm" type="button" @click="openConfigModal">编辑配置</button>
            <button class="btn btn-danger btn-sm" type="button" :disabled="!!busy" @click="confirmDialog = { visible: true, loading: false, action: 'delete-instance', entry: null }">删除实例</button>
          </div>
        </div>
        <p v-else class="plugin-config-empty">尚未部署。</p>
        <p v-if="selectedInstance && canWrite && formEmpty" class="plugin-config-empty">此插件没有宿主允许的可配置字段。</p>
        <p v-if="selectedInstance && !canWrite" class="plugin-config-empty">当前身份只有只读权限。</p>
      </section>

      <details class="plugin-ops" data-test="plugin-more">
        <summary>更多</summary>
        <div class="plugin-ops__stack">
          <section class="plugin-ops-panel">
            <header class="plugin-ops-panel__head">
              <div>
                <h2>管理操作</h2>
                <p>启用、停用、回滚，或导出脱敏诊断。还在运行的插件卸载时会先停用。</p>
              </div>
            </header>
            <div class="plugin-detail-actions">
              <button class="btn btn-secondary" type="button" @click="exportSafeDiagnostics">导出脱敏诊断</button>
              <template v-if="admin">
                <button class="btn btn-secondary" type="button" :disabled="!!busy" @click="handleLifecycleAction('enable')">启用</button>
                <button class="btn btn-secondary" type="button" :disabled="!!busy" @click="handleLifecycleAction('disable')">停用</button>
                <button class="btn btn-secondary" type="button" :disabled="!!busy || !detail.plugin.rollback_package_digest" @click="handleLifecycleAction('rollback')">回滚</button>
                <button
                  class="btn btn-danger"
                  type="button"
                  data-test="plugin-uninstall"
                  :disabled="!!busy"
                  :title="uninstallNeedsDisable ? '将先停用再卸载' : (hasPendingOperation ? '有未完成操作时仍可尝试卸载' : '卸载插件')"
                  @click="handleLifecycleAction('uninstall')"
                >卸载</button>
              </template>
            </div>
          </section>

          <div class="plugin-technical">
            <section class="plugin-ops-panel">
              <header class="plugin-ops-panel__head">
                <div>
                  <h2>插件包与风险</h2>
                  <p>签名、预算、权限差异，以及安装与运行边界。</p>
                </div>
              </header>
              <PluginPackageSummary :detail="detail.package" :source="source" :show-identity="false" :collapsible="false" />
              <PluginRiskNotices :package-detail="detail.package" :source="source" />
            </section>
          </div>

          <section v-if="showAgentExecutionStatus" class="plugin-ops-panel">
            <header class="plugin-ops-panel__head">
              <div>
                <h2>Agent 执行面状态、预算与故障</h2>
                <p>generation、同步、离线与执行故障只归属 Agent 执行面；失败时可重试。</p>
              </div>
            </header>
            <PluginAgentStatusTable :statuses="detail.agent_statuses" :agents="agents" :actionable="admin" :busy-agent="retryingAgent" @retry="retryAgent" />
          </section>

          <section v-if="selectedInstance" class="plugin-ops-panel">
            <header class="plugin-ops-panel__head">
              <div>
                <h2>实例 / Agent 运行日志</h2>
                <p>最近 5 条宿主持久化日志，按时间从新到旧。</p>
              </div>
            </header>
            <PluginLogViewer :plugin-id="detail.plugin.plugin_id" :instance-id="selectedInstance.id" :agents="selectedInstance.targets || []" />
          </section>

          <section class="plugin-ops-panel">
            <header class="plugin-ops-panel__head">
              <div>
                <h2>生命周期操作与审计</h2>
                <p>最近 5 条操作记录，失败项会保留脱敏后的原因。</p>
              </div>
            </header>
            <PluginOperationTimeline :operations="operations" />
          </section>
        </div>
      </details>

      <PluginDeployModal
        v-model="deployModalOpen"
        :plugin-id="detail.plugin.plugin_id"
        :instances="detail.instances"
        :instance="deployModalInstance"
        :published-entry="deployPublishedEntry"
        :intent="deployIntent"
        :can-submit="admin"
        :agents="agents"
        :resource-groups="visibleResourceGroups"
        :config-schema="detail.package?.config_schema || null"
        :document="deploymentDocument"
        :form-empty="formEmpty"
        :desired-lifecycle="detail.plugin.desired_lifecycle"
        :current-lifecycle="detail.plugin.current_lifecycle"
        :package-detail="detail.package"
        :declaresHTTPBackend="hasHTTPBackend"
        :faces="declaredFaces"
        :initial-face="selectedFaceID"
        :target-eligibility="targetEligibility"
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
        :hasHTTPBackend="hasHTTPBackend"
        :published-entries="publishedEntries"
        :agents="agents"
        :faces="declaredFaces"
        :initial-face="selectedFaceID"
        :can-write="canWrite"
        :can-publish="admin"
        :target-eligibility="targetEligibility"
        @saved="handleConfigSaved"
        @refreshed="handleConfigRefreshed"
        @delete-entry="startDeleteEntry"
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

.plugin-detail-empty-actions {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}
.back-link:hover { color: var(--color-primary); }

.plugin-detail-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }

.plugin-technical { display: grid; gap: var(--space-4); min-width: 0; }
.plugin-ops {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  overflow: hidden;
}
.plugin-ops > summary {
  list-style: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: 0.9rem 1.05rem;
  cursor: pointer;
  color: var(--color-text-primary);
  font-size: 0.9375rem;
  font-weight: 700;
}
.plugin-ops > summary::-webkit-details-marker { display: none; }
.plugin-ops > summary::after {
  content: '';
  width: 0.45rem;
  height: 0.45rem;
  border-right: 2px solid var(--color-text-tertiary);
  border-bottom: 2px solid var(--color-text-tertiary);
  transform: rotate(45deg);
  transition: transform 160ms var(--ease-default, cubic-bezier(0.4, 0, 0.2, 1));
}
.plugin-ops[open] > summary {
  border-bottom: 1px solid var(--color-border-subtle);
}
.plugin-ops[open] > summary::after {
  transform: rotate(225deg);
}
.plugin-ops__stack {
  display: grid;
  gap: var(--space-4);
  padding: 1rem 1.05rem 1.15rem;
}
.plugin-ops-panel {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  overflow: hidden;
  padding: 0.95rem 1rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: color-mix(in srgb, var(--color-bg-canvas) 55%, var(--color-bg-surface));
}
.plugin-ops-panel__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.plugin-ops-panel__head h2 {
  margin: 0;
  font-size: 0.9375rem;
  letter-spacing: -0.01em;
}
.plugin-ops-panel__head p {
  margin: 0.2rem 0 0;
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.45;
}
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

.plugin-http-entries {
  display: grid;
  gap: 0.75rem;
  padding-top: 0.15rem;
}
.plugin-http-entries__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.plugin-http-entries__head h2 {
  margin: 0;
  font-size: 0.8125rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  color: var(--color-text-secondary);
}
.plugin-http-entries__head p {
  margin: 0.2rem 0 0;
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.45;
}
.plugin-http-entries__count {
  flex-shrink: 0;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  line-height: 1.6;
}
.plugin-http-entries__list {
  display: grid;
  gap: 0.5rem;
  margin: 0;
  padding: 0;
  list-style: none;
}
.plugin-http-entry {
  display: flex;
  align-items: center;
  gap: 0.75rem 1rem;
  min-width: 0;
  padding: 0.85rem 1rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: color-mix(in srgb, var(--color-bg-canvas) 72%, var(--color-bg-surface));
}
.plugin-http-entry__main {
  display: grid;
  gap: 0.4rem;
  min-width: 0;
  flex: 1 1 16rem;
}
.plugin-http-entry__url {
  min-width: 0;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: 0.875rem;
  font-weight: 650;
  color: var(--color-text-primary);
  text-decoration: none;
  line-height: 1.35;
}
a.plugin-http-entry__url:hover {
  color: var(--color-primary);
}
.plugin-http-entry__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem 0.5rem;
}
.plugin-http-entry__node {
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}
.plugin-http-entry__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
  margin-left: auto;
}
.plugin-http-entries__add {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  width: 100%;
  min-height: 2.5rem;
  padding: 0.55rem 1rem;
  border: 1px dashed color-mix(in srgb, var(--color-border-default) 80%, var(--color-primary) 20%);
  border-radius: var(--radius-xl);
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-size: 0.8125rem;
  font-weight: 600;
  cursor: pointer;
}
.plugin-http-entries__add:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: color-mix(in srgb, var(--color-primary-subtle) 55%, transparent);
}
.plugin-http-entries__add:disabled {
  opacity: 0.48;
  cursor: not-allowed;
}

.plugin-section { display: grid; gap: var(--space-4); padding-top: var(--space-5); border-top: 1px solid var(--color-border-subtle); }
.plugin-section h2 { margin: 0; font-size: var(--text-lg); }
.plugin-section-heading { display: flex; align-items: start; justify-content: space-between; gap: var(--space-4); }
.plugin-section-heading > div { display: grid; gap: var(--space-1); }
.plugin-section-heading p { margin: 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.instance-facts { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); color: var(--color-text-muted); font-size: var(--text-sm); }
.plugin-face-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(15rem, 1fr)); gap: var(--space-3); width: 100%; }
.plugin-face {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}
.plugin-face__heading { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-3); }
.plugin-face__heading > div { display: grid; gap: 0.15rem; min-width: 0; }
.plugin-face__heading strong { color: var(--color-text-primary); font-size: var(--text-sm); }
.plugin-face__heading small { color: var(--color-text-tertiary); font-family: var(--font-mono); font-size: var(--text-xs); }
.plugin-face p { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); line-height: 1.5; }
.instance-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-left: auto; }

@media (max-width: 640px) {
  .plugin-section-heading { align-items: stretch; flex-direction: column; }
  .plugin-section-heading .btn { width: 100%; }
  .instance-actions { margin-left: 0; }
  .plugin-ops > summary,
  .plugin-ops__stack { padding-left: 0.85rem; padding-right: 0.85rem; }
  .plugin-ops-panel { padding: 0.8rem 0.85rem; }
  .plugin-detail-actions .btn { flex: 1 1 auto; }
  .plugin-http-entry { align-items: stretch; flex-direction: column; }
  .plugin-http-entry__actions { margin-left: 0; }
  .plugin-http-entry__actions .btn { width: 100%; }
}
</style>
