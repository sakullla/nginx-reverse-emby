<script setup>
import { computed } from 'vue'
import { schemaToUIComponents, stripWriteOnlyConfigValues } from '../../api/pluginSecurity'
import PluginDeclarativeUI from './PluginDeclarativeUI.vue'

const props = defineProps({
  schema: { type: Object, default: () => ({}) },
  config: { type: Object, default: () => ({}) },
  secretFields: { type: Array, default: () => [] },
  saving: { type: Boolean, default: false }
})
const emit = defineEmits(['submit'])

const components = computed(() => schemaToUIComponents(props.schema))
const document = computed(() => ({
  schema_version: 1,
  title: '',
  components: components.value,
  actions: [{ type: 'submit', id: 'save', label: props.saving ? '保存中…' : '保存配置并生成 revision' }]
}))
const config = computed(() => stripWriteOnlyConfigValues(props.schema, props.config))
</script>

<template>
  <div class="plugin-config-form">
    <p v-if="!components.length" class="plugin-config-form__empty">此插件没有宿主允许的可配置字段。</p>
    <PluginDeclarativeUI
      v-else
      :document="document"
      :config="config"
      :secret-fields="secretFields"
      :saving="saving"
      :can-configure="true"
      :can-act="false"
      @submit="emit('submit', $event)"
    />
  </div>
</template>

<style scoped>
.plugin-config-form { display: grid; gap: var(--space-4); }
.plugin-config-form__empty { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }
</style>
