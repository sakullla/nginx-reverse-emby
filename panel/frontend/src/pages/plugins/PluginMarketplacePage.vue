<script setup>
import { computed, onMounted, ref } from 'vue'
import { fetchRepositoryContents, fetchRepositorySources } from '../../api/pluginRepositories'
import { fetchPluginPackageDetail, fetchPlugins, installPlugin, upgradePlugin } from '../../api/plugins'
import PluginPackageSummary from '../../components/plugins/PluginPackageSummary.vue'
import PluginRiskNotices from '../../components/plugins/PluginRiskNotices.vue'

const loading = ref(true)
const actionBusy = ref(false)
const error = ref('')
const packages = ref([])
const installed = ref([])
const selected = ref(null)
const detail = ref(null)
const confirmed = ref(new Set())
const riskAccepted = ref(false)

const source = computed(() => selected.value?.source || {})
const installedPlugin = computed(() => installed.value.find((item) => item.plugin_id === selected.value?.plugin.id))
const isUpgrade = computed(() => installedPlugin.value && installedPlugin.value.active_package_digest !== selected.value?.plugin.sha256)
const requiredPermissions = computed(() => detail.value?.permissions || [])
const ready = computed(() => requiredPermissions.value.every((permission) => confirmed.value.has(permission)) && (source.value.kind === 'official' || riskAccepted.value))

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [sources, current] = await Promise.all([fetchRepositorySources(), fetchPlugins()])
    installed.value = current
    const contents = await Promise.all(sources.map(async (sourceItem) => ({ source: sourceItem, contents: await fetchRepositoryContents(sourceItem.id) })))
    packages.value = contents.flatMap(({ source: sourceItem, contents: value }) => {
      const entries = [...(value.entries || [])]
      if (value.directPlugin) entries.push(value.directPlugin)
      return entries.map((plugin) => ({ source: sourceItem, plugin }))
    })
    if (packages.value.length) await selectPackage(packages.value[0])
  } catch (cause) {
    error.value = cause?.message || '读取插件市场失败'
  } finally {
    loading.value = false
  }
}

async function selectPackage(item) {
  selected.value = item
  detail.value = null
  confirmed.value = new Set()
  riskAccepted.value = false
  error.value = ''
  try {
    detail.value = await fetchPluginPackageDetail(selection(false))
  } catch (cause) {
    error.value = cause?.message || '读取签名包详情失败'
  }
}

function selection(includeConfirmation = true) {
  const payload = {
    source_id: source.value.id,
    plugin_id: selected.value?.plugin.id,
    version: selected.value?.plugin.version,
    digest: selected.value?.plugin.sha256,
    confirmed_permissions: includeConfirmation ? [...confirmed.value].sort() : [],
    risk_accepted: includeConfirmation ? riskAccepted.value : false
  }
  return payload
}

function togglePermission(permission, checked) {
  const next = new Set(confirmed.value)
  if (checked) next.add(permission)
  else next.delete(permission)
  confirmed.value = next
}

async function applyPackage() {
  if (!ready.value || !selected.value) return
  actionBusy.value = true
  error.value = ''
  try {
    if (isUpgrade.value) await upgradePlugin(selected.value.plugin.id, selection())
    else await installPlugin(selection())
    installed.value = await fetchPlugins()
  } catch (cause) {
    error.value = cause?.message || '提交插件包失败'
  } finally {
    actionBusy.value = false
  }
}
</script>

<template>
  <main class="plugin-marketplace-page">
    <header class="page-header">
      <div><h1>插件市场</h1><p>只读取控制面验证后的签名包投影；市场内容不能向面板注入 HTML 或 JavaScript。</p></div>
      <RouterLink class="btn btn-secondary" to="/plugins/repositories">管理仓库源</RouterLink>
    </header>
    <p v-if="error" class="plugin-alert" role="alert">{{ error }}</p>
    <p v-if="loading">正在读取已验证市场快照…</p>
    <section v-else class="plugin-marketplace-workspace">
      <aside class="plugin-marketplace-list" aria-label="可安装插件">
        <button v-for="item in packages" :key="`${item.source.id}:${item.plugin.id}:${item.plugin.version}`" type="button" :class="{ active: item === selected }" @click="selectPackage(item)">
          <strong>{{ item.plugin.name || item.plugin.id }}</strong>
          <small>{{ item.plugin.version }} · {{ item.plugin.runtime?.kind || 'runtime 未声明' }}</small>
          <span>{{ item.source.kind === 'official' ? '官方' : '非官方' }} · {{ item.source.risk_label || '风险未标注' }}</span>
        </button>
        <p v-if="!packages.length">当前市场快照没有可用插件。</p>
      </aside>
      <div class="plugin-marketplace-detail">
        <template v-if="detail">
          <PluginPackageSummary :detail="detail" :source="source" />
          <PluginRiskNotices :package-detail="detail" :source="source" />
          <section class="permission-review">
            <h3>精确权限确认</h3>
            <p v-if="!requiredPermissions.length">此包未请求宿主能力。</p>
            <label v-for="permission in requiredPermissions" :key="permission">
              <input type="checkbox" :checked="confirmed.has(permission)" @change="togglePermission(permission, $event.target.checked)">
              <code>{{ permission }}</code>
            </label>
            <label v-if="source.kind !== 'official'" class="risk-confirmation">
              <input v-model="riskAccepted" type="checkbox">
              我已复核非官方来源、签名指纹、checksum、权限差异和宿主风险。
            </label>
            <p v-if="installedPlugin && !isUpgrade">当前 digest 已安装。</p>
            <p v-else-if="isUpgrade" class="upgrade-notice">升级将先验证候选 generation；失败时保留当前 active 版本。</p>
            <button class="btn btn-primary" type="button" :disabled="!ready || actionBusy || (installedPlugin && !isUpgrade)" @click="applyPackage">
              {{ actionBusy ? '提交中…' : isUpgrade ? '确认权限并升级' : '确认权限并安装' }}
            </button>
          </section>
        </template>
        <p v-else>选择一个插件查看宿主验证详情。</p>
      </div>
    </section>
  </main>
</template>

<style scoped>
.plugin-marketplace-page { max-width: 1280px; margin: 0 auto; }
.page-header { display: flex; justify-content: space-between; gap: var(--space-4); align-items: flex-start; }
h1 { margin: 0; } .page-header p { margin: var(--space-1) 0 0; color: var(--color-text-muted); }
.plugin-alert { color: var(--color-danger); }
.plugin-marketplace-workspace { display: grid; grid-template-columns: minmax(230px, 300px) minmax(0, 1fr); min-height: 560px; border: 1px solid var(--color-border-default); border-radius: var(--radius-xl); overflow: hidden; background: var(--color-bg-surface); }
.plugin-marketplace-list { border-right: 1px solid var(--color-border-subtle); background: var(--color-bg-subtle); }
.plugin-marketplace-list button { width: 100%; display: grid; gap: 3px; padding: var(--space-4); border: 0; border-bottom: 1px solid var(--color-border-subtle); background: transparent; color: var(--color-text-primary); text-align: left; cursor: pointer; }
.plugin-marketplace-list button.active { background: var(--color-bg-surface); box-shadow: inset 3px 0 var(--color-primary); }
.plugin-marketplace-list small, .plugin-marketplace-list span { color: var(--color-text-muted); font-size: var(--text-xs); }
.plugin-marketplace-detail { min-width: 0; display: grid; align-content: start; gap: var(--space-5); padding: var(--space-6); }
.permission-review { display: grid; gap: var(--space-3); padding-top: var(--space-4); border-top: 1px solid var(--color-border-subtle); }
.permission-review h3, .permission-review p { margin: 0; }.permission-review label { display: flex; gap: var(--space-2); align-items: flex-start; font-size: var(--text-sm); }
.upgrade-notice { color: var(--color-warning); }
@media (max-width: 800px) { .plugin-marketplace-workspace { grid-template-columns: 1fr; }.plugin-marketplace-list { border-right: 0; border-bottom: 1px solid var(--color-border-subtle); } }
</style>
