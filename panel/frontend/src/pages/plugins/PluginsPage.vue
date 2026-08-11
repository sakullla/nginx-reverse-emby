<script setup>
import { onMounted, ref } from 'vue'
import { fetchPluginDetail, fetchPlugins } from '../../api/plugins'
import { sanitizePluginText } from '../../api/pluginSecurity'
import { filterPluginDetailForActor, useAccessControl } from '../../context/useAccessControl'

const { actor, refreshActor } = useAccessControl()
const loading = ref(true)
const error = ref('')
const plugins = ref([])

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (!actor.value) await refreshActor()
    const summaries = await fetchPlugins()
    const details = await Promise.all(summaries.map((summary) => fetchPluginDetail(summary.plugin_id)))
    plugins.value = details.map((detail) => filterPluginDetailForActor(detail, actor.value)).filter(Boolean)
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '读取已安装插件失败')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="plugins-page">
    <header class="page-header">
      <div><h1>已安装插件</h1><p>实例按当前身份可见的资源组过滤；成员只能进入其授权资源组。</p></div>
      <RouterLink class="btn btn-primary" to="/plugins/marketplace">打开插件市场</RouterLink>
    </header>
    <p v-if="loading">正在读取插件状态…</p>
    <p v-else-if="error" role="alert">{{ error }}</p>
    <section v-else class="plugin-grid" aria-label="已安装插件">
      <RouterLink v-for="detail in plugins" :key="detail.plugin.plugin_id" :to="`/plugins/${encodeURIComponent(detail.plugin.plugin_id)}`" class="plugin-card">
        <div class="plugin-card__heading">
          <strong>{{ detail.package.manifest?.name || detail.plugin.plugin_id }}</strong>
          <span>{{ detail.plugin.current_lifecycle }}</span>
        </div>
        <p>{{ detail.plugin.plugin_id }} · {{ detail.package.version }}</p>
        <dl>
          <div><dt>Runtime</dt><dd>{{ detail.plugin.runtime_kind }} / {{ detail.plugin.runtime_abi }}</dd></div>
          <div><dt>可见实例</dt><dd>{{ detail.instances.length }}</dd></div>
          <div><dt>异常 Agent</dt><dd>{{ detail.agent_statuses.filter((status) => ['failed', 'degraded', 'crashed'].includes(status.runtime_state || status.current_state)).length }}</dd></div>
          <div><dt>来源风险</dt><dd>{{ detail.plugin.active_source_kind || 'unknown' }} · {{ detail.plugin.active_source_risk_label || '未标注' }}</dd></div>
        </dl>
      </RouterLink>
      <p v-if="!plugins.length">当前身份没有可见的插件实例。</p>
    </section>
  </main>
</template>

<style scoped>
.plugins-page { max-width: 1180px; margin: 0 auto; }.page-header { display: flex; justify-content: space-between; gap: var(--space-4); }.page-header h1 { margin: 0; }.page-header p { color: var(--color-text-muted); }
.plugin-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(290px, 1fr)); gap: var(--space-4); }
.plugin-card { display: grid; gap: var(--space-3); padding: var(--space-5); border: 1px solid var(--color-border-default); border-radius: var(--radius-xl); background: var(--color-bg-surface); color: var(--color-text-primary); text-decoration: none; }
.plugin-card:hover { border-color: var(--color-primary); }.plugin-card__heading { display: flex; justify-content: space-between; gap: var(--space-3); }.plugin-card__heading span { color: var(--color-primary); }
.plugin-card p { margin: 0; color: var(--color-text-muted); font-size: var(--text-sm); }.plugin-card dl { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-3); margin: 0; }dt { color: var(--color-text-muted); font-size: var(--text-xs); }dd { margin: 2px 0 0; font-size: var(--text-sm); overflow-wrap: anywhere; }
</style>
