<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { fetchAgents } from '../../api'
import { fetchResourceGroups } from '../../api/access'
import {
  configurePlugin,
  disablePlugin,
  enablePlugin,
  fetchPluginDetail,
  fetchPluginOperations,
  invokePluginDynamicAction,
  rollbackPlugin,
  uninstallPlugin
} from '../../api/plugins'
import { safePluginExport, sanitizePluginText, schemaToUIComponents, stripReadOnlyConfigValues, stripWriteOnlyConfigValues } from '../../api/pluginSecurity'
import { retryRevision } from '../../api/operations'
import { filterPluginDetailForActor, pickDefaultResourceGroupID, useAccessControl, visibleResourceGroupsForActor } from '../../context/useAccessControl'
import BaseTabs from '../../components/base/BaseTabs.vue'
import DeleteConfirmDialog from '../../components/DeleteConfirmDialog.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import PluginAgentStatusTable from '../../components/plugins/PluginAgentStatusTable.vue'
import PluginDeclarativeUI from '../../components/plugins/PluginDeclarativeUI.vue'
import PluginLogViewer from '../../components/plugins/PluginLogViewer.vue'
import PluginPackageSummary from '../../components/plugins/PluginPackageSummary.vue'
import PluginRiskNotices from '../../components/plugins/PluginRiskNotices.vue'
import PluginOperationTimeline from '../../components/operations/PluginOperationTimeline.vue'
import { getAgentStatus, getAgentStatusLabel } from '../../utils/agentHelpers'

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
const actionBusy = ref('')
const deploymentOpen = ref(false)
const deploymentError = ref('')
const deployment = reactive({ instanceID: '', resourceGroupID: '', targets: [] })

const admin = computed(() => can('*'))
const canWrite = computed(() => admin.value || can('resource.write'))
const selectedInstance = computed(() => detail.value?.instances.find((instance) => instance.id === selectedInstanceID.value) || null)
const selectedConfig = computed(() => selectedInstance.value?.pending_operation_id ? (selectedInstance.value.pending_config || {}) : (selectedInstance.value?.config || {}))
const selectedSecretFields = computed(() => selectedInstance.value?.pending_operation_id
  ? (selectedInstance.value.pending_secret_fields || [])
  : (selectedInstance.value?.secret_fields || []))
const source = computed(() => ({ kind: detail.value?.plugin.active_source_kind, risk_label: detail.value?.plugin.active_source_risk_label }))
const sortedAgents = computed(() => [...agents.value].sort((left, right) => String(left.name || left.id).localeCompare(String(right.name || right.id))))
const allDeploymentAgentsSelected = computed(() => sortedAgents.value.length > 0 && sortedAgents.value.every((agent) => deployment.targets.includes(agent.id)))
const visibleResourceGroups = computed(() => visibleResourceGroupsForActor(resourceGroups.value, actor.value))
const deploymentBlocker = computed(() => {
  if (!visibleResourceGroups.value.length) return '当前身份没有可见的资源组，无法部署。'
  if (!sortedAgents.value.length) return '当前没有可选择的节点。'
  if (!deployment.targets.length) return '请至少选择一个节点后再部署。'
  return ''
})
const sourceLabel = computed(() => source.value.kind === 'official' ? '官方来源' : '非官方来源')
const lifecycleLabel = computed(() => {
  const labels = { active: '生效中', degraded: '已降级', disabled: '已停用', upgrading: '升级中', applying: '应用中', rolling_back: '回滚中' }
  return labels[detail.value?.plugin?.current_lifecycle] || detail.value?.plugin?.current_lifecycle || '未知'
})
const deploymentStatusLabel = computed(() => (detail.value?.instances || []).length ? '已部署' : '尚未部署')

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
const canRenderForm = computed(() => {
  if (!selectedInstance.value) return false
  if (isDeclarativeUI.value) return admin.value || canWrite.value
  return admin.value
})
const formEmpty = computed(() => !isDeclarativeUI.value && !(uiDocument.value?.components?.length))

const confirmDialog = ref({ visible: false, loading: false, action: '' })
const pluginName = computed(() => detail.value?.package?.manifest?.name || detail.value?.plugin?.plugin_id || '')

const confirmCopy = computed(() => {
  switch (confirmDialog.value.action) {
    case 'disable':
      return { title: '确认停用插件', message: '停用后该插件将停止处理流量，依赖其防护的流量可能中断；可随时重新启用。', confirmText: '确认停用' }
    case 'rollback':
      return { title: '确认回滚插件', message: '回滚将把插件恢复到上一版本，并可能变更其权限。', confirmText: '确认回滚' }
    case 'uninstall':
    default:
      return { title: '确认卸载插件', message: '卸载将移除插件及其配置，此操作不可撤销。', confirmText: '确认卸载' }
  }
})

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
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
    if (!deployment.instanceID || visibleDetail.instances.some((instance) => instance.id === deployment.instanceID)) {
      deployment.instanceID = defaultDeploymentInstanceID(pluginID, visibleDetail.instances)
    }
    if (!visibleResourceGroups.value.some((group) => group.id === deployment.resourceGroupID)) {
      deployment.resourceGroupID = pickDefaultResourceGroupID(visibleResourceGroups.value)
    }
    if (!deployment.targets.length && agents.value.length === 1) deployment.targets = [agents.value[0].id]
    if (!visibleDetail.instances.length) deploymentOpen.value = true
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '读取插件详情失败')
  } finally {
    loading.value = false
  }
}

function defaultDeploymentInstanceID(pluginID, instances = []) {
  const normalized = String(pluginID || 'plugin').toLowerCase().replace(/[^a-z0-9._:/-]+/g, '-').replace(/^[^a-z0-9]+/, '') || 'plugin'
  const used = new Set(instances.map((instance) => instance.id))
  const first = `${normalized}-default`.slice(0, 128)
  if (!used.has(first)) return first
  for (let index = 2; index < 10000; index += 1) {
    const suffix = `-${index}`
    const candidate = `${normalized.slice(0, 128 - suffix.length)}${suffix}`
    if (!used.has(candidate)) return candidate
  }
  return first
}

function toggleDeployment() {
  if (!admin.value || busy.value) return
  deploymentError.value = ''
  deploymentOpen.value = !deploymentOpen.value
}

function toggleAllDeploymentAgents() {
  deployment.targets = allDeploymentAgentsSelected.value ? [] : sortedAgents.value.map((agent) => agent.id)
}

async function deployPlugin(payload) {
  if (!admin.value || busy.value) return
  deploymentError.value = ''
  const instanceID = defaultDeploymentInstanceID(detail.value.plugin.plugin_id, detail.value.instances)
  deployment.instanceID = instanceID
  const resourceGroupID = String(deployment.resourceGroupID || '').trim()
  const targets = [...new Set(deployment.targets.map((target) => String(target).trim()).filter(Boolean))]
  if (!visibleResourceGroups.value.some((group) => group.id === resourceGroupID)) {
    deploymentError.value = visibleResourceGroups.value.length ? '请选择一个可见的资源组。' : '当前身份没有可见的资源组，无法部署。'
    return
  }
  if (!targets.length) {
    deploymentError.value = '请至少选择一个节点后再部署。'
    return
  }
  busy.value = 'deploy'
  let configured = false
  try {
    const pluginID = detail.value.plugin.plugin_id
    const created = await configurePlugin(pluginID, {
      instance_id: instanceID,
      resource_group_id: resourceGroupID,
      targets,
      policy_chains: [],
      bindings: [],
      config: stripReadOnlyConfigValues(detail.value.package?.config_schema, payload.config),
      secret_replacements: payload.secret_replacements || {}
    })
    configured = true
    const lifecycle = detail.value.plugin
    if (lifecycle.desired_lifecycle !== 'enabled' && lifecycle.current_lifecycle !== 'active') await enablePlugin(pluginID)
    await load()
    selectedInstanceID.value = created?.id || instanceID
    deploymentOpen.value = false
  } catch (cause) {
    const message = sanitizePluginText(cause?.message || '部署插件实例失败')
    deploymentError.value = configured ? `配置已提交，但启用失败：${message}` : message
  } finally {
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
  if (!admin.value) return
  const action = confirmDialog.value.action
  confirmDialog.value.loading = true
  error.value = ''
  try {
    if (action === 'uninstall') {
      await uninstallPlugin(detail.value.plugin.plugin_id)
      await router.push('/plugins')
    } else {
      await lifecycle(action)
    }
  } catch (cause) {
    if (action === 'uninstall') {
      error.value = sanitizePluginText(cause?.message || '卸载插件失败')
    }
  } finally {
    confirmDialog.value = { visible: false, loading: false, action: '' }
  }
}

async function saveConfig(payload) {
  if (!admin.value || !selectedInstance.value) return
  busy.value = 'configure'
  error.value = ''
  try {
    const instance = selectedInstance.value
    await configurePlugin(detail.value.plugin.plugin_id, {
      instance_id: instance.id,
      resource_group_id: instance.resource_group_id,
      targets: instance.targets,
      policy_chains: instance.policy_chains || [],
      bindings: (instance.bindings || []).map((binding) => ({
        consumer: { kind: binding.consumer.kind, id: binding.consumer.id },
        target_agent_id: binding.target_agent_id
      })),
      config: stripReadOnlyConfigValues(detail.value.package?.config_schema, payload.config),
      secret_replacements: payload.secret_replacements || {}
    })
    await load()
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '保存插件配置失败')
  } finally {
    busy.value = ''
  }
}

async function runDynamicAction({ action, target_id, confirmed }) {
  if (!canWrite.value || !selectedInstance.value || actionBusy.value) return
  actionBusy.value = action.id
  error.value = ''
  try {
    await invokePluginDynamicAction(detail.value.plugin.plugin_id, selectedInstance.value.id, action.id, target_id, confirmed)
    await load()
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || `动态操作 ${action.label} 失败`)
  } finally {
    actionBusy.value = ''
  }
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
    const revision = status.desired_revision || status.target_revision
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
        <div class="page-header__right plugin-detail-actions">
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

      <section class="plugin-section">
        <div class="plugin-section-heading">
          <div>
            <h2>部署</h2>
            <p>选择资源组和节点，把插件部署到当前身份可见的范围。</p>
          </div>
          <button v-if="admin" class="btn btn-primary" type="button" :disabled="!!busy" @click="toggleDeployment">
            {{ deploymentOpen ? '收起部署表单' : '部署' }}
          </button>
        </div>

        <section v-if="deploymentOpen && admin" class="plugin-deployment" aria-label="部署插件实例">
          <div class="plugin-deployment__metadata">
            <label>
              <span>资源组</span>
              <select v-model="deployment.resourceGroupID" data-test="deployment-resource-group" :disabled="!visibleResourceGroups.length">
                <option v-if="!visibleResourceGroups.length" value="">暂无可见资源组</option>
                <option v-for="group in visibleResourceGroups" :key="group.id" :value="group.id">{{ group.name || group.id }}</option>
              </select>
            </label>
          </div>
          <fieldset class="plugin-deployment__agents">
            <legend>部署节点</legend>
            <div class="plugin-deployment__agent-actions">
              <span>已选择 {{ deployment.targets.length }} / {{ sortedAgents.length }}</span>
              <button class="btn btn-secondary" type="button" :disabled="!sortedAgents.length" @click="toggleAllDeploymentAgents">
                {{ allDeploymentAgentsSelected ? '清空选择' : '选择全部' }}
              </button>
            </div>
            <div v-if="sortedAgents.length" class="plugin-deployment__agent-grid">
              <label v-for="agent in sortedAgents" :key="agent.id" class="plugin-deployment__agent">
                <input v-model="deployment.targets" type="checkbox" :value="agent.id">
                <span>
                  <strong>{{ agent.name || agent.id }}</strong>
                  <small>{{ getAgentStatusLabel(getAgentStatus(agent)) }}</small>
                </span>
              </label>
            </div>
            <p v-else class="plugin-deployment__empty">当前没有可选择的节点。</p>
          </fieldset>
          <p v-if="deploymentError || deploymentBlocker" class="plugin-alert" role="alert">{{ deploymentError || deploymentBlocker }}</p>
          <div v-if="formEmpty" class="plugin-deployment__empty-config">
            <p class="plugin-config-empty">此插件没有需要先填写的配置，可直接部署。</p>
            <button class="btn btn-primary" type="button" :disabled="busy === 'deploy' || !!deploymentBlocker" @click="deployPlugin({ config: {}, secret_replacements: {} })">
              {{ busy === 'deploy' ? '部署中…' : '部署' }}
            </button>
          </div>
          <PluginDeclarativeUI
            v-else
            :document="deploymentDocument"
            :config="{}"
            :secret-fields="[]"
            :saving="busy === 'deploy' || !!deploymentBlocker"
            :can-configure="admin"
            :can-act="false"
            @submit="deployPlugin"
          />
        </section>

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
        </div>
        <template v-if="canRenderForm">
          <p v-if="formEmpty" class="plugin-config-empty">此插件没有宿主允许的可配置字段。</p>
          <PluginDeclarativeUI
            v-else
            :document="uiDocument"
            :config="formConfig"
            :secret-fields="selectedSecretFields"
            :saving="busy === 'configure'"
            :action-busy="!!actionBusy"
            :can-configure="admin"
            :can-act="canWrite"
            @submit="saveConfig"
            @dynamic="runDynamicAction"
          />
        </template>
        <p v-else-if="selectedInstance">当前身份只有只读权限。</p>
        <p v-else class="plugin-config-empty">尚未部署。</p>
      </section>

      <details class="plugin-technical">
        <summary>技术详情</summary>
        <PluginPackageSummary :detail="detail.package" :source="source" />
        <PluginRiskNotices :package-detail="detail.package" :source="source" />
      </details>

      <section class="plugin-section"><h2>逐 Agent 状态、预算与故障</h2><PluginAgentStatusTable :statuses="detail.agent_statuses" :actionable="admin" :busy-agent="retryingAgent" @retry="retryAgent" /></section>
      <section v-if="selectedInstance" class="plugin-section"><h2>实例 / Agent 运行日志</h2><PluginLogViewer :plugin-id="detail.plugin.plugin_id" :instance-id="selectedInstance.id" :agents="selectedInstance.targets || []" /></section>
      <section class="plugin-section"><h2>生命周期操作与审计</h2><PluginOperationTimeline :operations="operations" /></section>

      <DeleteConfirmDialog
        :show="confirmDialog.visible"
        :title="confirmCopy.title"
        :message="confirmCopy.message"
        :name="pluginName"
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

.plugin-detail-actions { flex-wrap: wrap; }

.plugin-alert { color: var(--color-danger); }
.plugin-technical { display: grid; gap: var(--space-4); }
.plugin-technical summary { cursor: pointer; color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-config-empty { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }

.plugin-section { display: grid; gap: var(--space-4); padding-top: var(--space-5); border-top: 1px solid var(--color-border-subtle); }
.plugin-section h2 { margin: 0; font-size: var(--text-lg); }
.plugin-section-heading { display: flex; align-items: start; justify-content: space-between; gap: var(--space-4); }
.plugin-section-heading > div { display: grid; gap: var(--space-1); }
.plugin-section-heading p { margin: 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.plugin-deployment { display: grid; gap: var(--space-5); padding: var(--space-5); border: 1px solid var(--color-border-default); border-radius: var(--radius-xl); background: var(--color-bg-subtle); }
.plugin-deployment__metadata { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-4); }
.plugin-deployment__metadata label { display: grid; gap: var(--space-2); color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-deployment__metadata input,
.plugin-deployment__metadata select { min-width: 0; padding: .65rem .75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); font: inherit; }
.plugin-deployment__agents { display: grid; gap: var(--space-3); min-width: 0; margin: 0; padding: 0; border: 0; }
.plugin-deployment__agents legend { margin-bottom: var(--space-2); color: var(--color-text-primary); font-weight: 600; }
.plugin-deployment__agent-actions { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); color: var(--color-text-muted); font-size: var(--text-sm); }
.plugin-deployment__agent-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: var(--space-3); }
.plugin-deployment__agent { display: flex; align-items: start; gap: var(--space-3); padding: var(--space-3); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); background: var(--color-bg-surface); cursor: pointer; }
.plugin-deployment__agent input { margin-top: .2rem; accent-color: var(--color-primary); }
.plugin-deployment__agent span { min-width: 0; display: grid; gap: 2px; }
.plugin-deployment__agent strong, .plugin-deployment__agent small { overflow-wrap: anywhere; }
.plugin-deployment__agent small, .plugin-deployment__empty { color: var(--color-text-muted); }
.plugin-deployment__empty { margin: 0; font-size: var(--text-sm); }
.plugin-deployment__empty-config { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.plugin-deployment__empty-config p { margin: 0; }
.instance-facts { display: flex; flex-wrap: wrap; gap: var(--space-3); color: var(--color-text-muted); font-size: var(--text-sm); }

@media (max-width: 700px) {
  .plugin-section-heading { align-items: stretch; flex-direction: column; }
  .plugin-section-heading .btn { width: 100%; }
  .plugin-deployment__metadata { grid-template-columns: 1fr; }
  .plugin-deployment__empty-config { align-items: stretch; flex-direction: column; }
}
</style>
