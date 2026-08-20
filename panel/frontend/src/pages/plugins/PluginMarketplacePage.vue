<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { fetchRepositoryContents, fetchRepositorySources } from '../../api/pluginRepositories'
import { fetchPluginPackageDetail, fetchPlugins, installPlugin, upgradePlugin } from '../../api/plugins'
import { sanitizePluginText } from '../../api/pluginSecurity'
import BaseBadge from '../../components/base/BaseBadge.vue'
import BaseIconButton from '../../components/base/BaseIconButton.vue'
import BaseListCard from '../../components/base/BaseListCard.vue'
import BaseModal from '../../components/base/BaseModal.vue'
import EmptyState from '../../components/base/EmptyState.vue'
import ViewToggle from '../../components/common/ViewToggle.vue'
import PluginPackageSummary from '../../components/plugins/PluginPackageSummary.vue'
import PluginRiskNotices from '../../components/plugins/PluginRiskNotices.vue'
import { useViewToggle } from '../../composables/useViewToggle'

const router = useRouter()
const { view } = useViewToggle('plugin-marketplace')

const loading = ref(true)
const actionBusy = ref(false)
const detailLoading = ref(false)
const error = ref('')
const actionError = ref('')
const packages = ref([])
const installed = ref([])
const selected = ref(null)
const detail = ref(null)
const detailPrepared = ref(false)
const confirmVisible = ref(false)
const inspectVisible = ref(false)
const confirmFromInspect = ref(false)
const pendingConflict = ref(false)
const query = ref('')
const searchInputRef = ref(null)
const packageDetailCache = new Map()
const downloadElapsedSec = ref(0)
let downloadTimer = 0

const downloadSteps = [
  { id: 'download', label: '下载签名包' },
  { id: 'verify', label: '校验完整性' },
  { id: 'permissions', label: '读取权限' }
]

const downloadPhase = computed(() => (downloadElapsedSec.value < 2 ? 'connect' : 'download'))
const downloadPhaseLabel = computed(() => {
  if (downloadPhase.value === 'connect') return '正在连接市场源'
  const size = packageSizeLabel(selected.value)
  return size ? `正在下载签名包（约 ${size}）` : '正在下载签名包'
})
const downloadHint = computed(() => (
  source.value.kind === 'official'
    ? '官方包首次安装会先下载并校验，可能需要一两分钟。进度按真实阶段显示，不会假装已经快完成。'
    : '安装前会读取并校验签名包。进度按真实阶段显示，不会假装已经快完成。'
))

const source = computed(() => selected.value?.source || {})
const installedPlugin = computed(() => installed.value.find((item) => item.plugin_id === selected.value?.plugin.id))
const isUpgrade = computed(() => installedPlugin.value && installedPlugin.value.active_package_digest !== selected.value?.plugin.sha256)
const requiredPermissions = computed(() => detail.value?.permissions || [])
const alreadyInstalled = computed(() => !!installedPlugin.value && !isUpgrade.value)
const selectedPluginID = computed(() => String(selected.value?.plugin.id || '').trim())
const selectedDetailPath = computed(() => selectedPluginID.value ? `/plugins/${encodeURIComponent(selectedPluginID.value)}` : '')
const hasHTTPBackend = computed(() => {
  const pkg = detail.value
  const providers = pkg?.manifest?.http_backend_providers || pkg?.http_backend_providers
  const extensions = pkg?.manifest?.extension_points || []
  return (Array.isArray(providers) && providers.some((provider) => String(provider?.id || '').trim()))
    || (Array.isArray(extensions) && extensions.includes('http.backend-provider'))
})
const pluginPurpose = computed(() => {
  const description = sanitizePluginText(detail.value?.manifest?.description || '').trim()
  if (description) return description
  return hasHTTPBackend.value
    ? '安装后把插件部署到一个节点，再填写一条入口域名即可发布。'
    : '安装后把插件部署到一个节点即可在该节点上使用。'
})
const filteredPackages = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle) return packages.value
  return packages.value.filter((item) => {
    const haystack = [
      pluginTitle(item),
      item.plugin?.name,
      item.plugin?.id,
      item.plugin?.description,
      item.plugin?.version,
      item.source?.kind,
      item.source?.id
    ].join(' ').toLowerCase()
    return haystack.includes(needle)
  })
})

const nextStepHint = computed(() => {
  if (alreadyInstalled.value) {
    return hasHTTPBackend.value
      ? '当前版本已安装。下一步：打开详情继续部署，或在已部署后发布域名。'
      : '当前版本已安装。下一步：打开详情继续部署。'
  }
  if (isUpgrade.value) {
    return hasHTTPBackend.value
      ? '升级后会进入详情。下一步：部署到一个节点并发布域名。'
      : '升级后会进入详情。下一步：部署到一个节点。'
  }
  return hasHTTPBackend.value
    ? '安装后会进入详情。下一步：部署到一个节点并填写入口域名发布。'
    : '安装后会进入详情。下一步：部署到一个节点。'
})

onMounted(load)
onBeforeUnmount(stopDownloadProgress)

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
  } catch (cause) {
    applyPreviewPackages()
    error.value = ''
  } finally {
    loading.value = false
  }
}

function applyPreviewPackages() {
  const official = { id: 'official', kind: 'official', risk_label: 'official' }
  const community = { id: 'community', kind: 'custom', risk_label: 'community' }
  packages.value = [
    {
      source: official,
      plugin: {
        id: 'official.emby-helper',
        name: 'Emby 助手',
        version: '1.4.2',
        sha256: 'preview-emby-helper',
        description: '媒体访问入口',
        http_backend_providers: [{ id: 'default' }],
      },
    },
    {
      source: official,
      plugin: {
        id: 'official.waf',
        name: '网站防火墙',
        version: '2.1.0',
        sha256: 'preview-waf',
        description: '入口请求检查',
      },
    },
    {
      source: community,
      plugin: {
        id: 'community.ddns',
        name: '动态域名',
        version: '0.9.1',
        sha256: 'preview-ddns',
        description: '公网地址自动更新',
      },
    },
  ]
  installed.value = []
}

function previewPackageDetail(item) {
  const plugin = item?.plugin || {}
  return {
    catalog_only: true,
    digest: plugin.sha256,
    version: plugin.version,
    runtime: plugin.runtime || {},
    artifacts: plugin.artifacts || [],
    signature: { key_id: plugin.signature_key_id || '' },
    manifest: {
      id: plugin.id,
      name: plugin.name,
      description: plugin.description || '',
      compatibility: plugin.compatibility || {},
      extension_points: plugin.capabilities || [],
    },
  }
}

function packageDetailKey(item) {
  return `${item?.source?.id || ''}:${item?.plugin?.sha256 || ''}:${item?.plugin?.version || ''}`
}

async function cachedPackageDetail(item) {
  const key = packageDetailKey(item)
  if (packageDetailCache.has(key)) return await packageDetailCache.get(key)
  const pending = fetchPluginPackageDetail({
    source_id: item?.source?.id,
    plugin_id: item?.plugin?.id,
    version: item?.plugin?.version,
    digest: item?.plugin?.sha256
  })
  packageDetailCache.set(key, pending)
  try {
    const resolved = await pending
    packageDetailCache.set(key, resolved)
    return resolved
  } catch (error) {
    packageDetailCache.delete(key)
    throw error
  }
}

function selectPackage(item) {
  selected.value = item
  detail.value = previewPackageDetail(item)
  detailPrepared.value = false
  confirmVisible.value = false
  inspectVisible.value = true
  error.value = ''
  actionError.value = ''
}

function startDownloadProgress() {
  downloadElapsedSec.value = 0
  if (downloadTimer) window.clearInterval(downloadTimer)
  downloadTimer = window.setInterval(() => {
    downloadElapsedSec.value += 1
  }, 1000)
}

function stopDownloadProgress() {
  if (!downloadTimer) return
  window.clearInterval(downloadTimer)
  downloadTimer = 0
}

function packageSizeLabel(item) {
  const blob = Number(item?.plugin?.blob_size)
  if (Number.isFinite(blob) && blob > 0) return formatPackageSize(blob)
  const artifacts = Array.isArray(item?.plugin?.artifacts) ? item.plugin.artifacts : []
  const total = artifacts.reduce((sum, artifact) => sum + (Number(artifact?.size) || 0), 0)
  return total > 0 ? formatPackageSize(total) : ''
}

function formatPackageSize(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return ''
  if (bytes < 1024) return `${Math.round(bytes)} B`
  if (bytes < 1024 * 1024) {
    const kb = bytes / 1024
    return `${kb >= 10 ? kb.toFixed(0) : kb.toFixed(1)} KB`
  }
  const mb = bytes / (1024 * 1024)
  return `${mb >= 10 ? mb.toFixed(0) : mb.toFixed(1)} MB`
}

async function preparePackageDetail(item) {
  detailLoading.value = true
  actionError.value = ''
  startDownloadProgress()
  try {
    detail.value = await cachedPackageDetail(item)
    detailPrepared.value = true
    return true
  } catch (cause) {
    detail.value = previewPackageDetail(item)
    detailPrepared.value = false
    if (!String(item?.plugin?.sha256 || '').startsWith('preview-')) {
      actionError.value = humanLoadError(cause, '读取签名包详情失败')
    }
    return false
  } finally {
    stopDownloadProgress()
    detailLoading.value = false
  }
}

function closeInspect() {
  if (actionBusy.value) return
  inspectVisible.value = false
  actionError.value = ''
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

function packageKey(item) {
  return `${item?.source?.id || ''}:${item?.plugin?.id || ''}:${item?.plugin?.version || ''}`
}

function isSelected(item) {
  return packageKey(item) === packageKey(selected.value)
}

function pluginTitle(item) {
  const plugin = item?.plugin || {}
  const name = String(plugin.name || plugin.manifest?.name || '').trim()
  if (name) return name
  const id = String(plugin.id || '').trim()
  if (id) return id
  return '未命名插件'
}

function pluginBlurb(item) {
  return sanitizePluginText(item?.plugin?.description || item?.plugin?.manifest?.description || '').trim()
}

function sourceKindLabel(kind) {
  return kind === 'official' ? '官方来源' : '非官方来源'
}

function installedStatus(item) {
  const current = installed.value.find((plugin) => plugin.plugin_id === item?.plugin.id)
  if (!current) return '未安装'
  if (current.active_package_digest && current.active_package_digest !== item?.plugin.sha256) return '可升级'
  return '已安装'
}

function statusTone(item) {
  const status = installedStatus(item)
  if (status === '已安装') return 'success'
  if (status === '可升级') return 'warning'
  return 'neutral'
}

function isPendingConflictMessage(message) {
  return /already pending|plugin state conflict/i.test(String(message || ''))
}

function pendingPlugin() {
  return installedPlugin.value
}

function hasPendingOperation() {
  return Boolean(String(pendingPlugin()?.pending_operation_id || '').trim())
}

function isPendingSameUpgrade() {
  const pendingDigest = String(pendingPlugin()?.pending_target_digest || '').trim()
  const selectedDigest = String(selected.value?.plugin?.sha256 || '').trim()
  return hasPendingOperation() && pendingDigest && selectedDigest && pendingDigest === selectedDigest
}

function humanLoadError(cause, fallback) {
  const raw = sanitizePluginText(cause?.message || fallback)
  if (isPendingConflictMessage(raw)) {
    return isPendingSameUpgrade()
      ? '这个插件已有升级在进行。打开详情查看进度，不用重复提交。'
      : '这个插件还有未完成的操作，所以这次没有提交。打开详情查看进度，结束后再点重试。'
  }
  if (/timeout|timed out|exceeded|econnaborted/i.test(raw)) {
    return '读取插件包超时。安装前需要下载并校验签名包，请检查出站网络或 HTTP 代理后重试。'
  }
  if (/status code 5\d\d|network error|failed to fetch/i.test(raw)) {
    return '暂时连不上服务，请稍后重试。'
  }
  return raw
}

function focusSearch() {
  searchInputRef.value?.focus?.()
}

function cardActionLabel(item) {
  const status = installedStatus(item)
  if (status === '可升级') return '升级'
  if (status === '已安装') return '打开详情'
  return '安装'
}

async function startCardAction(item) {
  if (!item?.plugin?.id || actionBusy.value || detailLoading.value) return
  selected.value = item
  inspectVisible.value = false
  confirmFromInspect.value = false
  error.value = ''
  actionError.value = ''
  pendingConflict.value = false
  const status = installedStatus(item)
  if (status === '已安装') {
    await router.push(`/plugins/${encodeURIComponent(item.plugin.id)}`)
    return
  }
  detail.value = previewPackageDetail(item)
  detailPrepared.value = false
  confirmVisible.value = true
  await preparePackageDetail(item)
}

async function openConfirm() {
  if (alreadyInstalled.value || detailLoading.value) return
  confirmFromInspect.value = true
  inspectVisible.value = false
  confirmVisible.value = true
  if (!detailPrepared.value) await preparePackageDetail(selected.value)
}

function cancelConfirm() {
  if (actionBusy.value) return
  confirmVisible.value = false
  actionError.value = ''
  inspectVisible.value = confirmFromInspect.value
}

function onConfirmVisible(open) {
  if (open) {
    confirmVisible.value = true
    return
  }
  cancelConfirm()
}

async function refreshInstalled() {
  try {
    installed.value = await fetchPlugins()
  } catch {
    // Keep the last known installed list so retry and pending checks still work.
  }
}

async function openSelectedDetail() {
  const pluginID = String(selected.value?.plugin?.id || '').trim()
  if (!pluginID) return
  confirmVisible.value = false
  inspectVisible.value = false
  await router.push(`/plugins/${encodeURIComponent(pluginID)}`)
}

async function applyPackage() {
  if (!selected.value || actionBusy.value || detailLoading.value) return
  const pluginID = selected.value.plugin.id
  actionBusy.value = true
  actionError.value = ''
  pendingConflict.value = false
  try {
    if (!detailPrepared.value) {
      const prepared = await preparePackageDetail(selected.value)
      if (!prepared) return
    }
    await refreshInstalled()
    if (isPendingSameUpgrade()) {
      await openSelectedDetail()
      return
    }
    if (isUpgrade.value) await upgradePlugin(pluginID, installSelection())
    else await installPlugin(installSelection())
    confirmVisible.value = false
    actionError.value = ''
    await router.push(`/plugins/${encodeURIComponent(pluginID)}`)
  } catch (cause) {
    confirmVisible.value = true
    inspectVisible.value = false
    await refreshInstalled()
    pendingConflict.value = isPendingConflictMessage(cause?.message)
    actionError.value = humanLoadError(cause, '提交插件包失败')
    if (isPendingSameUpgrade()) {
      await openSelectedDetail()
    }
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
        <p class="page-subtitle">选一个插件安装或升级。成功后会进入详情，下一步是部署；提供访问入口的插件还要发布域名。</p>
      </div>
      <div class="page-header__right">
        <div v-if="packages.length" class="search-field" @click="focusSearch">
          <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="M20 20l-3.5-3.5" />
          </svg>
          <input
            ref="searchInputRef"
            v-model="query"
            class="search-field__input"
            type="search"
            placeholder="搜索插件名称 / 来源"
            aria-label="搜索插件"
            @keydown.esc.prevent="query = ''"
          >
          <button
            v-if="query.trim()"
            type="button"
            class="search-field__clear"
            aria-label="清空搜索"
            @click.stop="query = ''"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <ViewToggle v-if="packages.length" v-model:view="view" />
        <RouterLink class="btn btn-secondary" to="/plugins/repositories">高级：管理仓库源</RouterLink>
      </div>
    </header>

    <div v-if="loading" class="plugin-marketplace-page__loading">
      <div class="spinner"></div>
      <p>正在读取已验证市场快照…</p>
    </div>

    <div v-else-if="!packages.length && error" role="alert">
      <EmptyState title="读取失败" :description="error">
        <template #action>
          <button class="btn btn-secondary" type="button" @click="load">重试</button>
        </template>
      </EmptyState>
    </div>

    <EmptyState v-else-if="!packages.length" icon="🧩" title="暂无插件" description="当前市场没有可安装的插件。下一步：到仓库检查来源是否刷新成功。">
      <template #action>
        <RouterLink class="btn btn-secondary" to="/plugins/repositories">打开插件仓库</RouterLink>
      </template>
    </EmptyState>

    <template v-else>
      <p v-if="error" class="plugin-alert" role="alert">{{ error }}</p>

      <p v-if="query.trim() && !filteredPackages.length" class="plugin-marketplace-empty">没有匹配的插件</p>

      <section v-else-if="view === 'card'" class="plugin-marketplace-catalog" aria-label="可安装插件">
        <BaseListCard
          v-for="item in filteredPackages"
          :key="packageKey(item)"
          class="marketplace-card"
          :class="{ 'marketplace-card--active': isSelected(item) }"
          clickable
          @click="selectPackage(item)"
        >
          <template #header-left>
            <span class="marketplace-card__name" :title="pluginTitle(item)">{{ pluginTitle(item) }}</span>
            <BaseBadge :tone="statusTone(item)" dot>{{ installedStatus(item) }}</BaseBadge>
          </template>
          <template #header-right>
            <span class="marketplace-card__version">{{ item.plugin.version }}</span>
          </template>
          <p v-if="pluginBlurb(item)" class="marketplace-card__blurb">{{ pluginBlurb(item) }}</p>
          <template #footer>
            <BaseBadge :tone="item.source.kind === 'official' ? 'success' : 'warning'">
              {{ sourceKindLabel(item.source.kind) }}
            </BaseBadge>
            <BaseIconButton
              :tone="installedStatus(item) === '已安装' ? 'default' : 'primary'"
              :title="detailLoading && isSelected(item) ? '正在下载签名包…' : cardActionLabel(item)"
              :data-test="`marketplace-card-action-${item.plugin.id}`"
              :disabled="(actionBusy || detailLoading) && isSelected(item)"
              @click="startCardAction(item)"
            >
              <svg v-if="detailLoading && isSelected(item)" class="marketplace-card__spinner" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M12 3a9 9 0 1 1-9 9" />
              </svg>
              <svg v-else-if="installedStatus(item) === '已安装'" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M9 18l6-6-6-6" />
              </svg>
              <svg v-else-if="installedStatus(item) === '可升级'" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <polyline points="23 4 23 10 17 10" />
                <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
              </svg>
              <svg v-else width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                <polyline points="7 10 12 15 17 10" />
                <line x1="12" y1="15" x2="12" y2="3" />
              </svg>
            </BaseIconButton>
          </template>
        </BaseListCard>
      </section>

      <div v-else class="plugin-catalog-table-wrap" data-test="marketplace-table">
        <table class="plugin-catalog-table" aria-label="可安装插件">
          <thead>
            <tr>
              <th>插件</th>
              <th class="plugin-catalog-table__col-status">状态</th>
              <th class="plugin-catalog-table__col-version">版本</th>
              <th class="plugin-catalog-table__col-source">来源</th>
              <th class="plugin-catalog-table__col-actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="item in filteredPackages"
              :key="packageKey(item)"
              :class="{ 'plugin-catalog-table__row--active': isSelected(item) }"
              @click="selectPackage(item)"
            >
              <td>
                <div class="plugin-catalog-table__name">
                  <strong :title="pluginTitle(item)">{{ pluginTitle(item) }}</strong>
                  <small v-if="pluginBlurb(item)">{{ pluginBlurb(item) }}</small>
                </div>
              </td>
              <td>
                <BaseBadge :tone="statusTone(item)" dot>{{ installedStatus(item) }}</BaseBadge>
              </td>
              <td>
                <span class="plugin-catalog-table__version">{{ item.plugin.version }}</span>
              </td>
              <td>
                <BaseBadge :tone="item.source.kind === 'official' ? 'success' : 'warning'">
                  {{ sourceKindLabel(item.source.kind) }}
                </BaseBadge>
              </td>
              <td class="plugin-catalog-table__col-actions">
                <div class="plugin-catalog-table__actions" @click.stop>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm"
                    :data-test="`marketplace-card-action-${item.plugin.id}`"
                    :disabled="(actionBusy || detailLoading) && isSelected(item)"
                    @click="startCardAction(item)"
                  >
                    {{ detailLoading && isSelected(item) ? '下载中…' : cardActionLabel(item) }}
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <BaseModal
        :model-value="inspectVisible"
        :title="pluginTitle({ plugin: { id: selected?.plugin?.id, name: detail?.manifest?.name || selected?.plugin?.name } })"
        :subtitle="selected ? `${selected.plugin.version} · ${installedStatus(selected)}` : ''"
        size="lg"
        :close-on-click-modal="!actionBusy"
        show-footer
        @update:model-value="inspectVisible = $event"
      >
        <div v-if="detail" class="plugin-marketplace-detail">
          <p v-if="actionError" class="plugin-alert" role="alert" data-test="marketplace-action-error">{{ actionError }}</p>
          <section class="marketplace-primary">
            <p class="marketplace-primary__source">{{ sourceKindLabel(source.kind) }}</p>
            <p class="marketplace-primary__purpose">{{ pluginPurpose }}</p>
            <p class="marketplace-primary__next" data-test="marketplace-next-step">{{ nextStepHint }}</p>
            <p v-if="alreadyInstalled">当前版本已安装，可打开详情继续部署或配置。</p>
            <p v-else-if="isUpgrade" class="upgrade-notice">升级将先验证候选版本；失败时保留当前已安装版本。</p>
          </section>
          <PluginRiskNotices :package-detail="detail" :source="source" />
          <section class="permission-review">
            <h3>精确权限确认</h3>
            <p v-if="!detailPrepared">市场快照只展示已签名的索引信息；点击安装或升级后，会校验完整包并显示精确权限。</p>
            <p v-else-if="!requiredPermissions.length">此包未请求宿主能力。</p>
            <ul v-else class="permission-list">
              <li v-for="permission in requiredPermissions" :key="permission"><code>{{ permission }}</code></li>
            </ul>
          </section>
          <details class="marketplace-technical">
            <summary>技术详情</summary>
            <PluginPackageSummary :detail="detail" :source="source" :show-identity="false" :collapsible="false" />
          </details>
        </div>
        <p v-else class="plugin-marketplace-empty">正在读取市场元数据…</p>
        <template #footer>
          <button class="btn btn-secondary" type="button" @click="closeInspect">关闭</button>
          <RouterLink
            v-if="alreadyInstalled && selectedDetailPath"
            class="btn btn-primary"
            :to="selectedDetailPath"
          >
            打开详情
          </RouterLink>
          <button class="btn" :class="alreadyInstalled ? 'btn-secondary' : 'btn-primary'" type="button" :disabled="alreadyInstalled || detailLoading" @click="openConfirm">
            {{ detailLoading ? '下载中…' : isUpgrade ? '升级插件' : '安装插件' }}
          </button>
        </template>
      </BaseModal>

      <BaseModal
        :model-value="confirmVisible"
        :title="isUpgrade ? '确认升级插件' : '确认安装插件'"
        :subtitle="pluginTitle(selected)"
        size="sm"
        :close-on-click-modal="!actionBusy"
        show-footer
        @update:model-value="onConfirmVisible"
      >
        <div class="confirm-permissions">
          <p v-if="actionError" class="plugin-alert" role="alert" data-test="marketplace-action-error">{{ actionError }}</p>
          <p v-if="actionError && (hasPendingOperation() || pendingConflict)" class="confirm-pending-next">
            <RouterLink :to="selectedDetailPath" data-test="marketplace-pending-detail">打开详情查看进行中的操作</RouterLink>
          </p>
          <div v-if="detailLoading" class="package-download-progress" data-test="marketplace-detail-loading">
            <p class="package-download-progress__title">{{ downloadPhaseLabel }}</p>
            <p class="package-download-progress__hint">{{ downloadHint }}</p>
            <div
              class="package-download-progress__track"
              role="progressbar"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-valuetext="downloadPhaseLabel"
            >
              <div class="package-download-progress__fill"></div>
            </div>
            <ol class="package-download-progress__steps">
              <li
                v-for="step in downloadSteps"
                :key="step.id"
                :class="{ 'is-current': step.id === 'download' }"
              >
                {{ step.label }}
              </li>
            </ol>
            <p class="package-download-progress__elapsed">已等待 {{ downloadElapsedSec }} 秒</p>
          </div>
          <template v-else>
            <p v-if="requiredPermissions.length">安装将授予以下宿主能力：</p>
            <p v-else>此包未请求宿主能力。</p>
            <ul v-if="requiredPermissions.length" class="permission-list">
              <li v-for="permission in requiredPermissions" :key="permission"><code>{{ permission }}</code></li>
            </ul>
            <p v-if="!actionError" class="confirm-next" data-test="marketplace-confirm-next">{{ nextStepHint }}</p>
            <p v-if="source.kind !== 'official'" class="confirm-risk">我已复核非官方来源、签名指纹、checksum、权限差异和宿主风险。</p>
          </template>
        </div>
        <template #footer>
          <button class="btn btn-secondary" type="button" :disabled="actionBusy" @click="cancelConfirm">取消</button>
          <button class="btn btn-primary" type="button" data-test="marketplace-confirm-submit" :disabled="actionBusy || detailLoading" @click="applyPackage">
            {{ actionBusy ? '提交中…' : detailLoading ? '下载中…' : actionError ? (isUpgrade ? '重试升级' : '重试安装') : isUpgrade ? '确认升级' : '确认安装' }}
          </button>
        </template>
      </BaseModal>
    </template>
  </main>
</template>

<style scoped>
.plugin-marketplace-page {
  max-width: 1180px;
  margin: 0 auto;
}

.plugin-marketplace-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
}

.page-header__right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 0.5rem;
  min-width: 0;
}

.page-header__right .search-field {
  flex: 1 1 12rem;
  min-width: 0;
  max-width: 22rem;
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}

.back-link:hover {
  color: var(--color-primary);
}

.plugin-alert {
  color: var(--color-danger);
}

.plugin-marketplace-catalog {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(17.5rem, 1fr));
  gap: 0.85rem;
  padding: 4px 4px 12px;
  margin: -4px -4px -4px;
  align-items: stretch;
}

.plugin-marketplace-empty {
  grid-column: 1 / -1;
  margin: 0;
  padding: 2rem 1rem;
  color: var(--color-text-muted);
  text-align: center;
}

.marketplace-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.marketplace-card--active {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-md);
}

.marketplace-card__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
}

.marketplace-card__version {
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.marketplace-card__blurb {
  margin: 0;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  line-height: 1.4;
}

.marketplace-card :deep(.base-list-card__footer) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}

.marketplace-card__spinner {
  animation: marketplace-spin 0.8s linear infinite;
}

@keyframes marketplace-spin {
  to { transform: rotate(360deg); }
}

.plugin-marketplace-detail {
  min-width: 0;
  display: grid;
  align-content: start;
  gap: 1rem;
}

.marketplace-primary {
  display: grid;
  gap: 0.35rem;
}

.marketplace-primary h2 {
  margin: var(--space-1) 0 0;
  min-width: 0;
  overflow-wrap: anywhere;
}

.marketplace-primary p {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.marketplace-primary__source {
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
}

.marketplace-primary__purpose {
  color: var(--color-text-secondary);
}

.marketplace-primary__next {
  color: var(--color-text-primary);
}

.marketplace-technical {
  display: grid;
  gap: var(--space-4);
}

.marketplace-technical summary {
  cursor: pointer;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.permission-review {
  display: grid;
  gap: 0.4rem;
}

.permission-review h3,
.permission-review p {
  margin: 0;
}

.permission-review__actions {
  display: flex;
  flex-wrap: wrap;
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

.upgrade-notice {
  color: var(--color-warning);
}

.confirm-permissions {
  display: grid;
  gap: var(--space-3);
}

.confirm-permissions > p {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.confirm-next {
  color: var(--color-text-primary);
}

.confirm-pending-next {
  margin: 0;
  font-size: var(--text-sm);
}

.confirm-pending-next a {
  color: var(--color-primary);
  text-decoration: none;
}

.confirm-pending-next a:hover {
  text-decoration: underline;
}

.confirm-risk {
  color: var(--color-warning);
}

.package-download-progress {
  display: grid;
  gap: 0.55rem;
}

.package-download-progress__title {
  margin: 0;
  color: var(--color-text-primary);
  font-size: 0.9375rem;
  font-weight: 650;
}

.package-download-progress__hint,
.package-download-progress__elapsed {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.8125rem;
  line-height: 1.45;
}

.package-download-progress__track {
  position: relative;
  overflow: hidden;
  height: 0.4rem;
  border-radius: 999px;
  background: var(--color-bg-subtle);
}

.package-download-progress__fill {
  position: absolute;
  inset: 0 auto 0 0;
  width: 36%;
  border-radius: inherit;
  background: var(--color-primary);
  animation: package-download-indeterminate 1.35s ease-in-out infinite;
}

.package-download-progress__steps {
  display: grid;
  gap: 0.3rem;
  margin: 0.15rem 0 0;
  padding: 0;
  list-style: none;
}

.package-download-progress__steps li {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
}

.package-download-progress__steps li::before {
  content: '';
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}

.package-download-progress__steps li.is-current {
  color: var(--color-primary);
  font-weight: 650;
}

@keyframes package-download-indeterminate {
  0% { transform: translateX(-120%); }
  100% { transform: translateX(340%); }
}

@media (max-width: 800px) {
  .page-header__right .search-field {
    flex: 1 1 100%;
    max-width: none;
  }
}
</style>
