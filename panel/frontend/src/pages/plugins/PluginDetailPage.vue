<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
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
import { safePluginExport, sanitizePluginText, schemaToUIComponents, stripWriteOnlyConfigValues } from '../../api/pluginSecurity'
import { retryRevision } from '../../api/operations'
import { filterPluginDetailForActor, useAccessControl } from '../../context/useAccessControl'
import BaseTabs from '../../components/base/BaseTabs.vue'
import DeleteConfirmDialog from '../../components/DeleteConfirmDialog.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import PluginAgentStatusTable from '../../components/plugins/PluginAgentStatusTable.vue'
import PluginDeclarativeUI from '../../components/plugins/PluginDeclarativeUI.vue'
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
const selectedInstanceID = ref('')
const retryingAgent = ref('')
const actionBusy = ref('')

const admin = computed(() => can('*'))
const canWrite = computed(() => admin.value || can('resource.write'))
const selectedInstance = computed(() => detail.value?.instances.find((instance) => instance.id === selectedInstanceID.value) || null)
const selectedConfig = computed(() => selectedInstance.value?.pending_operation_id ? (selectedInstance.value.pending_config || {}) : (selectedInstance.value?.config || {}))
const selectedSecretFields = computed(() => selectedInstance.value?.pending_operation_id
  ? (selectedInstance.value.pending_secret_fields || [])
  : (selectedInstance.value?.secret_fields || []))
const source = computed(() => ({ kind: detail.value?.plugin.active_source_kind, risk_label: detail.value?.plugin.active_source_risk_label }))

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
    actions: [{ type: 'submit', id: 'save', label: busy.value === 'configure' ? '保存中…' : '保存配置并生成 revision' }]
  }
})
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
    const [nextDetail, nextOperations] = await Promise.all([fetchPluginDetail(pluginID), fetchPluginOperations(pluginID)])
    const visibleDetail = filterPluginDetailForActor(nextDetail, actor.value)
    if (!visibleDetail) throw new Error('当前身份无权查看此插件的资源组实例')
    detail.value = visibleDetail
    operations.value = nextOperations
    selectedInstanceID.value = visibleDetail.instances[0]?.id || ''
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '读取插件详情失败')
  } finally {
    loading.value = false
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
      config: payload.config,
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
          <p class="page-subtitle">{{ detail.plugin.current_lifecycle }} · state v{{ detail.plugin.state_version }}</p>
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
      <PluginPackageSummary :detail="detail.package" :source="source" />
      <PluginRiskNotices :package-detail="detail.package" :source="source" />

      <section class="plugin-section">
        <h2>实例配置</h2>
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
      </section>

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
.plugin-config-empty { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }

.plugin-section { display: grid; gap: var(--space-4); padding-top: var(--space-5); border-top: 1px solid var(--color-border-subtle); }
.plugin-section h2 { margin: 0; font-size: var(--text-lg); }
.instance-facts { display: flex; flex-wrap: wrap; gap: var(--space-3); color: var(--color-text-muted); font-size: var(--text-sm); }
</style>
