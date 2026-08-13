<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { fetchRepositoryContents, fetchRepositorySources } from '../../api/pluginRepositories'
import { fetchPluginPackageDetail, fetchPlugins, installPlugin, upgradePlugin } from '../../api/plugins'
import { sanitizePluginText } from '../../api/pluginSecurity'
import BaseModal from '../../components/base/BaseModal.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import PluginPackageSummary from '../../components/plugins/PluginPackageSummary.vue'
import PluginRiskNotices from '../../components/plugins/PluginRiskNotices.vue'

const router = useRouter()

const loading = ref(true)
const actionBusy = ref(false)
const error = ref('')
const packages = ref([])
const installed = ref([])
const selected = ref(null)
const detail = ref(null)
const confirmVisible = ref(false)

const source = computed(() => selected.value?.source || {})
const installedPlugin = computed(() => installed.value.find((item) => item.plugin_id === selected.value?.plugin.id))
const isUpgrade = computed(() => installedPlugin.value && installedPlugin.value.active_package_digest !== selected.value?.plugin.sha256)
const requiredPermissions = computed(() => detail.value?.permissions || [])
const alreadyInstalled = computed(() => !!installedPlugin.value && !isUpgrade.value)

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
    error.value = sanitizePluginText(cause?.message || '读取插件市场失败')
  } finally {
    loading.value = false
  }
}

async function selectPackage(item) {
  selected.value = item
  detail.value = null
  confirmVisible.value = false
  error.value = ''
  try {
    detail.value = await fetchPluginPackageDetail(packageSelection())
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '读取签名包详情失败')
  }
}

function packageSelection() {
  return {
    source_id: source.value.id,
    plugin_id: selected.value?.plugin.id,
    version: selected.value?.plugin.version,
    digest: selected.value?.plugin.sha256
  }
}

function installSelection() {
  return {
    ...packageSelection(),
    confirmed_permissions: [...requiredPermissions.value].sort(),
    risk_accepted: source.value.kind !== 'official'
  }
}

function installedStatus(item) {
  const current = installed.value.find((plugin) => plugin.plugin_id === item?.plugin.id)
  if (!current) return '未安装'
  if (current.active_package_digest && current.active_package_digest !== item?.plugin.sha256) return '可升级'
  return '已安装'
}

function openConfirm() {
  if (alreadyInstalled.value) return
  confirmVisible.value = true
}

function cancelConfirm() {
  if (actionBusy.value) return
  confirmVisible.value = false
}

async function applyPackage() {
  if (!selected.value || actionBusy.value) return
  actionBusy.value = true
  error.value = ''
  try {
    const pluginID = selected.value.plugin.id
    if (isUpgrade.value) await upgradePlugin(pluginID, installSelection())
    else await installPlugin(installSelection())
    confirmVisible.value = false
    await router.push(`/plugins/${encodeURIComponent(pluginID)}`)
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '提交插件包失败')
  } finally {
    actionBusy.value = false
  }
}
</script>

<template>
  <main class="plugin-marketplace-page">
    <header class="page-header">
      <div class="page-header__left">
        <RouterLink to="/plugins" class="back-link">← 已安装插件</RouterLink>
        <h1 class="page-title">插件市场</h1>
        <p class="page-subtitle">浏览已验证的插件，安装或升级后会进入详情继续部署。</p>
      </div>
      <div class="page-header__right">
        <RouterLink class="btn btn-secondary" to="/plugins/repositories">管理仓库源</RouterLink>
      </div>
    </header>

    <div v-if="loading" class="plugin-marketplace-page__loading">
      <div class="spinner"></div>
      <p>正在读取已验证市场快照…</p>
    </div>

    <div v-else-if="!packages.length && error" role="alert">
      <EmptyState title="读取失败" :description="error" />
    </div>

    <EmptyState v-else-if="!packages.length" icon="🧩" title="暂无插件" description="当前市场快照没有可用插件。" />

    <template v-else>
      <p v-if="error" class="plugin-alert" role="alert">{{ error }}</p>

      <section class="plugin-marketplace-workspace">
        <aside class="plugin-marketplace-list" aria-label="可安装插件">
          <button v-for="item in packages" :key="`${item.source.id}:${item.plugin.id}:${item.plugin.version}`" type="button" :class="{ active: item === selected }" @click="selectPackage(item)">
            <strong>{{ item.plugin.name || item.plugin.id }}</strong>
            <small>{{ item.plugin.version }} · {{ item.source.kind === 'official' ? '官方来源' : '非官方来源' }}</small>
            <span>{{ installedStatus(item) }}</span>
          </button>
        </aside>

        <div class="plugin-marketplace-detail">
          <template v-if="detail">
            <section class="marketplace-primary">
              <div>
                <p class="marketplace-primary__source">{{ source.kind === 'official' ? '官方来源' : '非官方来源' }}</p>
                <h2>{{ detail.manifest?.name || selected?.plugin.name || selected?.plugin.id }}</h2>
                <p>{{ selected?.plugin.version }} · {{ installedStatus(selected) }}</p>
              </div>
              <div class="permission-review__actions">
                <button class="btn btn-primary" type="button" :disabled="alreadyInstalled" @click="openConfirm">
                  {{ isUpgrade ? '升级插件' : '安装插件' }}
                </button>
              </div>
              <p v-if="alreadyInstalled">当前版本已安装，可打开详情继续部署或配置。</p>
              <p v-else-if="isUpgrade" class="upgrade-notice">升级将先验证候选版本；失败时保留当前已安装版本。</p>
            </section>
            <details class="marketplace-technical">
              <summary>技术详情</summary>
              <PluginPackageSummary :detail="detail" :source="source" />
              <PluginRiskNotices :package-detail="detail" :source="source" />
              <section class="permission-review">
                <h3>精确权限确认</h3>
                <p v-if="!requiredPermissions.length">此包未请求宿主能力。</p>
                <ul v-else class="permission-list">
                  <li v-for="permission in requiredPermissions" :key="permission"><code>{{ permission }}</code></li>
                </ul>
              </section>
            </details>
          </template>
          <EmptyState v-else icon="🧩" title="选择插件查看详情" description="从左侧列表选择一个插件查看宿主验证详情。" />
        </div>
      </section>

      <BaseModal
        v-model="confirmVisible"
        :title="isUpgrade ? '确认升级插件' : '确认安装插件'"
        :subtitle="selected?.plugin.name || selected?.plugin.id"
        :close-on-click-modal="!actionBusy"
        show-footer
      >
        <div class="confirm-permissions">
          <p v-if="requiredPermissions.length">安装将授予以下宿主能力：</p>
          <p v-else>此包未请求宿主能力。</p>
          <ul v-if="requiredPermissions.length" class="permission-list">
            <li v-for="permission in requiredPermissions" :key="permission"><code>{{ permission }}</code></li>
          </ul>
          <p v-if="source.kind !== 'official'" class="confirm-risk">我已复核非官方来源、签名指纹、checksum、权限差异和宿主风险。</p>
        </div>
        <template #footer>
          <button class="btn btn-secondary" type="button" :disabled="actionBusy" @click="cancelConfirm">取消</button>
          <button class="btn btn-primary" type="button" :disabled="actionBusy" @click="applyPackage">
            {{ actionBusy ? '提交中…' : isUpgrade ? '确认升级' : '确认安装' }}
          </button>
        </template>
      </BaseModal>
    </template>
  </main>
</template>

<style scoped>
.plugin-marketplace-page { max-width: 1280px; margin: 0 auto; }

.plugin-marketplace-page__loading {
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

.plugin-alert { color: var(--color-danger); }

.plugin-marketplace-workspace {
  display: grid;
  grid-template-columns: minmax(230px, 300px) minmax(0, 1fr);
  min-height: 560px;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  overflow: hidden;
  background: var(--color-bg-surface);
}

.plugin-marketplace-list {
  border-right: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}
.plugin-marketplace-list button {
  width: 100%;
  display: grid;
  gap: 3px;
  padding: var(--space-4);
  border: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  background: transparent;
  color: var(--color-text-primary);
  text-align: left;
  cursor: pointer;
}
.plugin-marketplace-list button.active {
  background: var(--color-bg-surface);
  box-shadow: inset 3px 0 var(--color-primary);
}
.plugin-marketplace-list small,
.plugin-marketplace-list span {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.plugin-marketplace-detail {
  min-width: 0;
  display: grid;
  align-content: start;
  gap: var(--space-5);
  padding: var(--space-6);
}

.marketplace-primary { display: grid; gap: var(--space-3); }
.marketplace-primary h2 { margin: var(--space-1) 0 0; }
.marketplace-primary p { margin: 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.marketplace-primary__source { color: var(--color-text-secondary); font-size: var(--text-xs); }
.marketplace-technical { display: grid; gap: var(--space-4); }
.marketplace-technical summary { cursor: pointer; color: var(--color-text-secondary); font-size: var(--text-sm); }
.permission-review {
  display: grid;
  gap: var(--space-3);
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
}
.permission-review h3,
.permission-review p {
  margin: 0;
}
.permission-review__actions {
  display: flex;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

.permission-list {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding-left: 1.2rem;
}
.permission-list code {
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.upgrade-notice { color: var(--color-warning); }

.confirm-permissions { display: grid; gap: var(--space-3); }
.confirm-permissions p { margin: 0; color: var(--color-text-secondary); font-size: var(--text-sm); }
.confirm-risk { color: var(--color-warning); }

@media (max-width: 800px) {
  .plugin-marketplace-workspace { grid-template-columns: 1fr; }
  .plugin-marketplace-list { border-right: 0; border-bottom: 1px solid var(--color-border-subtle); }
  .permission-review__actions { flex-direction: column; }
  .permission-review__actions > .btn { width: 100%; }
}
</style>
