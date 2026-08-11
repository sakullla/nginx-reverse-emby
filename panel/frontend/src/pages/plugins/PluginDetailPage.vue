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
import { safePluginExport, sanitizePluginText } from '../../api/pluginSecurity'
import { retryRevision } from '../../api/operations'
import { filterPluginDetailForActor, useAccessControl } from '../../context/useAccessControl'
import PluginAgentStatusTable from '../../components/plugins/PluginAgentStatusTable.vue'
import PluginConfigForm from '../../components/plugins/PluginConfigForm.vue'
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
const source = computed(() => ({ kind: detail.value?.plugin.active_source_kind, risk_label: detail.value?.plugin.active_source_risk_label }))

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
  if (!window.confirm(`确认执行插件 ${action} 操作？`)) return
  busy.value = action
  error.value = ''
  try {
    const pluginID = detail.value.plugin.plugin_id
    if (action === 'enable') await enablePlugin(pluginID)
    else if (action === 'disable') await disablePlugin(pluginID)
    else if (action === 'rollback') await rollbackPlugin(pluginID, detail.value.package.permissions || [])
    else if (action === 'uninstall') {
      await uninstallPlugin(pluginID)
      await router.push('/plugins')
      return
    }
    await load()
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || `插件 ${action} 失败`)
  } finally {
    busy.value = ''
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
    <p v-if="loading">正在读取插件详情…</p>
    <p v-else-if="error && !detail" role="alert">{{ error }}</p>
    <template v-else-if="detail">
      <header class="page-header">
        <div><RouterLink to="/plugins">← 已安装插件</RouterLink><h1>{{ detail.package.manifest?.name || detail.plugin.plugin_id }}</h1><p>{{ detail.plugin.current_lifecycle }} · state v{{ detail.plugin.state_version }}</p></div>
        <div class="page-actions">
          <button class="btn btn-secondary" type="button" @click="exportSafeDiagnostics">导出脱敏诊断</button>
          <template v-if="admin">
            <button class="btn btn-secondary" type="button" :disabled="!!busy" @click="lifecycle('enable')">启用</button>
            <button class="btn btn-secondary" type="button" :disabled="!!busy" @click="lifecycle('disable')">停用</button>
            <button class="btn btn-secondary" type="button" :disabled="!!busy || !detail.plugin.rollback_package_digest" @click="lifecycle('rollback')">回滚</button>
            <button class="btn plugin-danger" type="button" :disabled="!!busy || detail.plugin.current_lifecycle !== 'disabled'" @click="lifecycle('uninstall')">卸载</button>
          </template>
        </div>
      </header>
      <p v-if="error" class="plugin-alert" role="alert">{{ error }}</p>
      <PluginPackageSummary :detail="detail.package" :source="source" />
      <PluginRiskNotices :package-detail="detail.package" :source="source" />
      <section class="plugin-section">
        <h2>实例配置</h2>
        <select v-if="detail.instances.length" v-model="selectedInstanceID">
          <option v-for="instance in detail.instances" :key="instance.id" :value="instance.id">{{ instance.id }} · {{ instance.resource_group_id }}</option>
        </select>
        <div v-if="selectedInstance" class="instance-facts">
          <span>目标：{{ selectedInstance.targets.join(', ') || '—' }}</span>
          <span>版本：{{ selectedInstance.config_version }}</span>
          <span>状态：{{ selectedInstance.current_state }}</span>
        </div>
        <PluginDeclarativeUI v-if="selectedInstance && canWrite && detail.package.declarative_ui" :document="detail.package.declarative_ui" :config="selectedInstance.config" :saving="busy === 'configure'" :action-busy="!!actionBusy" @submit="saveConfig" @dynamic="runDynamicAction" />
        <PluginConfigForm v-else-if="selectedInstance && admin" :schema="detail.package.config_schema" :config="selectedInstance.config" :saving="busy === 'configure'" @submit="saveConfig" />
        <p v-else-if="selectedInstance">当前身份只有只读权限。</p>
      </section>
      <section class="plugin-section"><h2>逐 Agent 状态、预算与故障</h2><PluginAgentStatusTable :statuses="detail.agent_statuses" :actionable="admin" :busy-agent="retryingAgent" @retry="retryAgent" /></section>
      <section v-if="selectedInstance" class="plugin-section"><h2>实例 / Agent 运行日志</h2><PluginLogViewer :plugin-id="detail.plugin.plugin_id" :instance-id="selectedInstance.id" :agents="selectedInstance.targets || []" /></section>
      <section class="plugin-section"><h2>生命周期操作与审计</h2><PluginOperationTimeline :operations="operations" /></section>
    </template>
  </main>
</template>

<style scoped>
.plugin-detail-page { max-width: 1180px; display: grid; gap: var(--space-6); margin: 0 auto; }.page-header { display: flex; justify-content: space-between; gap: var(--space-4); }.page-header h1 { margin: var(--space-2) 0 0; }.page-header p { margin: var(--space-1) 0 0; color: var(--color-text-muted); }.page-actions { display: flex; flex-wrap: wrap; align-content: flex-start; justify-content: flex-end; gap: var(--space-2); }
.plugin-alert, .plugin-danger { color: var(--color-danger); }.plugin-section { display: grid; gap: var(--space-4); padding-top: var(--space-5); border-top: 1px solid var(--color-border-subtle); }.plugin-section h2 { margin: 0; font-size: var(--text-lg); }.plugin-section select { max-width: 30rem; padding: .6rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); }.instance-facts { display: flex; flex-wrap: wrap; gap: var(--space-3); color: var(--color-text-muted); font-size: var(--text-sm); }
</style>
