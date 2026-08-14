<script setup>
import { ref, watch } from 'vue'
import { configurePlugin, invokePluginDynamicAction } from '../../api/plugins'
import { sanitizePluginText, stripReadOnlyConfigValues } from '../../api/pluginSecurity'
import BaseModal from '../base/BaseModal.vue'
import PluginDeclarativeUI from './PluginDeclarativeUI.vue'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  pluginId: { type: String, required: true },
  instance: { type: Object, default: null },
  document: { type: Object, required: true },
  config: { type: Object, default: () => ({}) },
  secretFields: { type: Array, default: () => [] },
  configSchema: { type: Object, default: null },
  canWrite: { type: Boolean, default: false }
})
const emit = defineEmits(['update:modelValue', 'saved', 'refreshed'])

const busy = ref(false)
const actionBusy = ref(false)
const error = ref('')

watch(() => props.modelValue, (open) => {
  if (open) error.value = ''
})

async function save(payload) {
  if (!props.canWrite || !props.instance || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const instance = props.instance
    await configurePlugin(props.pluginId, {
      instance_id: instance.id,
      resource_group_id: instance.resource_group_id,
      targets: instance.targets,
      policy_chains: instance.policy_chains || [],
      bindings: (instance.bindings || []).map((binding) => ({
        consumer: { kind: binding.consumer.kind, id: binding.consumer.id },
        target_agent_id: binding.target_agent_id
      })),
      config: stripReadOnlyConfigValues(props.configSchema, payload.config),
      secret_replacements: payload.secret_replacements || {}
    })
    emit('saved')
    emit('update:modelValue', false)
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '保存插件配置失败')
  } finally {
    busy.value = false
  }
}

async function runDynamicAction({ action, target_id, confirmed }) {
  if (!props.canWrite || !props.instance || actionBusy.value) return
  actionBusy.value = true
  error.value = ''
  try {
    await invokePluginDynamicAction(props.pluginId, props.instance.id, action.id, target_id, confirmed)
    emit('refreshed')
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || `动态操作 ${action.label} 失败`)
  } finally {
    actionBusy.value = false
  }
}
</script>

<template>
  <BaseModal
    :model-value="modelValue"
    :title="`编辑配置 · ${instance?.id || ''}`"
    :subtitle="instance ? `资源组 ${instance.resource_group_id} · 版本 ${instance.config_version}` : ''"
    size="lg"
    :close-on-click-modal="false"
    data-test="plugin-instance-config-modal"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <section class="plugin-instance-config" aria-label="编辑实例配置">
      <p v-if="error" class="plugin-alert" role="alert">{{ error }}</p>
      <PluginDeclarativeUI
        v-if="instance"
        :document="document"
        :config="config"
        :secret-fields="secretFields"
        :saving="busy"
        :action-busy="actionBusy"
        :can-configure="canWrite"
        :can-act="canWrite"
        @submit="save"
        @dynamic="runDynamicAction"
      />
    </section>
  </BaseModal>
</template>

<style scoped>
.plugin-instance-config { display: grid; gap: var(--space-4); min-width: 0; }
.plugin-alert { margin: 0; color: var(--color-danger); font-size: var(--text-sm); }
</style>
