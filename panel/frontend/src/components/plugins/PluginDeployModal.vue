<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { configurePlugin, enablePlugin } from '../../api/plugins'
import { sanitizePluginText, stripReadOnlyConfigValues } from '../../api/pluginSecurity'
import { pickDefaultResourceGroupID, resourceGroupDisplayName } from '../../context/useAccessControl'
import BaseModal from '../base/BaseModal.vue'
import PluginDeclarativeUI from './PluginDeclarativeUI.vue'
import { getAgentStatus, getAgentStatusLabel } from '../../utils/agentHelpers'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  pluginId: { type: String, required: true },
  instances: { type: Array, default: () => [] },
  agents: { type: Array, default: () => [] },
  resourceGroups: { type: Array, default: () => [] },
  configSchema: { type: Object, default: null },
  document: { type: Object, required: true },
  formEmpty: { type: Boolean, default: false },
  desiredLifecycle: { type: String, default: '' },
  currentLifecycle: { type: String, default: '' }
})
const emit = defineEmits(['update:modelValue', 'deployed'])

const busy = ref(false)
const error = ref('')
const deployment = reactive({ resourceGroupID: '', targets: [] })

const sortedAgents = computed(() => [...props.agents].sort((left, right) => String(left.name || left.id).localeCompare(String(right.name || right.id))))
const allAgentsSelected = computed(() => sortedAgents.value.length > 0 && sortedAgents.value.every((agent) => deployment.targets.includes(agent.id)))
const blocker = computed(() => {
  if (!props.resourceGroups.length) return '当前身份没有可见的资源组，无法部署。'
  if (!sortedAgents.value.length) return '当前没有可选择的节点。'
  if (!deployment.targets.length) return '请至少选择一个节点后再部署。'
  return ''
})

watch(() => props.modelValue, (open) => {
  if (!open) return
  error.value = ''
  if (!props.resourceGroups.some((group) => group.id === deployment.resourceGroupID)) {
    deployment.resourceGroupID = pickDefaultResourceGroupID(props.resourceGroups)
  }
  if (!deployment.targets.length && props.agents.length === 1) deployment.targets = [props.agents[0].id]
})

function toggleAllAgents() {
  deployment.targets = allAgentsSelected.value ? [] : sortedAgents.value.map((agent) => agent.id)
}

function defaultInstanceID() {
  const normalized = String(props.pluginId || 'plugin').toLowerCase().replace(/[^a-z0-9._:/-]+/g, '-').replace(/^[^a-z0-9]+/, '') || 'plugin'
  const used = new Set(props.instances.map((instance) => instance.id))
  const first = `${normalized}-default`.slice(0, 128)
  if (!used.has(first)) return first
  for (let index = 2; index < 10000; index += 1) {
    const suffix = `-${index}`
    const candidate = `${normalized.slice(0, 128 - suffix.length)}${suffix}`
    if (!used.has(candidate)) return candidate
  }
  return first
}

async function deploy(payload) {
  if (busy.value) return
  error.value = ''
  const instanceID = defaultInstanceID()
  const resourceGroupID = String(deployment.resourceGroupID || '').trim()
  const targets = [...new Set(deployment.targets.map((target) => String(target).trim()).filter(Boolean))]
  if (!props.resourceGroups.some((group) => group.id === resourceGroupID)) {
    error.value = props.resourceGroups.length ? '请选择一个可见的资源组。' : '当前身份没有可见的资源组，无法部署。'
    return
  }
  if (!targets.length) {
    error.value = '请至少选择一个节点后再部署。'
    return
  }
  busy.value = true
  let configured = false
  try {
    const created = await configurePlugin(props.pluginId, {
      instance_id: instanceID,
      resource_group_id: resourceGroupID,
      targets,
      policy_chains: [],
      bindings: [],
      config: stripReadOnlyConfigValues(props.configSchema, payload.config),
      secret_replacements: payload.secret_replacements || {}
    })
    configured = true
    if (props.desiredLifecycle !== 'enabled' && props.currentLifecycle !== 'active') await enablePlugin(props.pluginId)
    emit('deployed', created?.id || instanceID)
    emit('update:modelValue', false)
  } catch (cause) {
    const message = sanitizePluginText(cause?.message || '部署插件实例失败')
    error.value = configured ? `配置已提交，但启用失败：${message}` : message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <BaseModal
    :model-value="modelValue"
    title="部署插件实例"
    subtitle="选择资源组和节点，把插件部署到当前身份可见的范围。"
    size="lg"
    :close-on-click-modal="false"
    data-test="plugin-deploy-modal"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <section class="plugin-deployment" aria-label="部署插件实例">
      <div class="plugin-deployment__metadata">
        <label>
          <span>资源组</span>
          <select v-model="deployment.resourceGroupID" data-test="deployment-resource-group" :disabled="!resourceGroups.length">
            <option v-if="!resourceGroups.length" value="">暂无可见资源组</option>
            <option v-for="group in resourceGroups" :key="group.id" :value="group.id">{{ resourceGroupDisplayName(group) }}</option>
          </select>
        </label>
      </div>
      <fieldset class="plugin-deployment__agents">
        <legend>部署节点</legend>
        <div class="plugin-deployment__agent-actions">
          <span>已选择 {{ deployment.targets.length }} / {{ sortedAgents.length }}</span>
          <button class="btn btn-secondary btn-sm" type="button" :disabled="!sortedAgents.length" @click="toggleAllAgents">
            {{ allAgentsSelected ? '清空选择' : '选择全部' }}
          </button>
        </div>
        <div v-if="sortedAgents.length" class="plugin-deployment__agent-grid">
          <label v-for="agent in sortedAgents" :key="agent.id" class="plugin-deployment__agent" :class="{ 'plugin-deployment__agent--selected': deployment.targets.includes(agent.id) }">
            <input v-model="deployment.targets" type="checkbox" :value="agent.id">
            <span>
              <strong>{{ agent.name || agent.id }}</strong>
              <small>{{ getAgentStatusLabel(getAgentStatus(agent)) }}</small>
            </span>
          </label>
        </div>
        <p v-else class="plugin-deployment__empty">当前没有可选择的节点。</p>
      </fieldset>
      <p v-if="error || blocker" class="plugin-alert" role="alert">{{ error || blocker }}</p>
      <div v-if="formEmpty" class="plugin-deployment__empty-config">
        <p class="plugin-config-empty">此插件没有需要先填写的配置，可直接部署。</p>
        <button class="btn btn-primary" type="button" :disabled="busy || !!blocker" @click="deploy({ config: {}, secret_replacements: {} })">
          {{ busy ? '部署中…' : '部署' }}
        </button>
      </div>
      <PluginDeclarativeUI
        v-else
        :document="document"
        :config="{}"
        :secret-fields="[]"
        :saving="busy || !!blocker"
        :can-configure="true"
        :can-act="false"
        @submit="deploy"
      />
    </section>
  </BaseModal>
</template>

<style scoped>
.plugin-deployment { display: grid; gap: var(--space-5); min-width: 0; }
.plugin-deployment__metadata { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-4); }
.plugin-deployment__metadata label { display: grid; gap: var(--space-2); color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-deployment__metadata select { min-width: 0; padding: .6rem .75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); font: inherit; }
.plugin-deployment__metadata select:focus-visible { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 18%, transparent); }
.plugin-deployment__agents { display: grid; gap: var(--space-3); min-width: 0; margin: 0; padding: 0; border: 0; }
.plugin-deployment__agents legend { margin-bottom: var(--space-2); color: var(--color-text-primary); font-weight: 600; font-size: var(--text-sm); }
.plugin-deployment__agent-actions { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); color: var(--color-text-muted); font-size: var(--text-sm); }
.plugin-deployment__agent-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: var(--space-3); }
.plugin-deployment__agent {
  display: flex; align-items: start; gap: var(--space-3); padding: var(--space-3);
  border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg);
  background: var(--color-bg-surface); cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default), background var(--duration-fast) var(--ease-default);
}
.plugin-deployment__agent:hover { border-color: var(--color-border-default); }
.plugin-deployment__agent--selected { border-color: var(--color-primary); background: color-mix(in srgb, var(--color-primary) 6%, var(--color-bg-surface)); }
.plugin-deployment__agent input { margin-top: .2rem; accent-color: var(--color-primary); }
.plugin-deployment__agent span { min-width: 0; display: grid; gap: 2px; }
.plugin-deployment__agent strong, .plugin-deployment__agent small { overflow-wrap: anywhere; }
.plugin-deployment__agent small, .plugin-deployment__empty { color: var(--color-text-muted); }
.plugin-deployment__empty { margin: 0; font-size: var(--text-sm); }
.plugin-deployment__empty-config { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.plugin-deployment__empty-config p { margin: 0; }
.plugin-alert { margin: 0; color: var(--color-danger); font-size: var(--text-sm); }
.plugin-config-empty { color: var(--color-text-muted); font-size: var(--text-xs); }

@media (max-width: 640px) {
  .plugin-deployment__metadata { grid-template-columns: 1fr; }
  .plugin-deployment__empty-config { align-items: stretch; flex-direction: column; }
}
</style>
