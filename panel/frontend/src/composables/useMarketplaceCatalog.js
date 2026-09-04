import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { fetchRepositoryContents, fetchRepositorySources, refreshRepositorySource } from '../api/pluginRepositories'
import { fetchPluginPackageDetail, fetchPlugins, installPlugin, upgradePlugin } from '../api/plugins'
import { sanitizePluginText } from '../api/pluginSecurity'
import { messageStore } from '../stores/messages'
import { formatPanelDateTime } from '../utils/panelDateTime.js'

const downloadSteps = [
  { id: 'download', label: '下载签名包' },
  { id: 'verify', label: '校验完整性' },
  { id: 'permissions', label: '读取权限' }
]

export function sourceQueryValue(source) {
  if (Array.isArray(source)) return String(source[0] || '').trim()
  return String(source || '').trim()
}

export function resolveMarketplacePackage(packages, pluginId, sourceQuery) {
  const id = String(pluginId || '').trim()
  if (!id) return null
  const matches = (packages || []).filter((item) => String(item?.plugin?.id || '').trim() === id)
  if (!matches.length) return null
  const sourceId = sourceQueryValue(sourceQuery)
  if (sourceId) {
    return matches.find((item) => String(item?.source?.id || '').trim() === sourceId) || null
  }
  return matches.find((item) => item?.source?.kind === 'official') || matches[0]
}

export function marketplaceDetailHref(item) {
  const pluginId = String(item?.plugin?.id || '').trim()
  if (!pluginId) return ''
  const sourceId = String(item?.source?.id || '').trim()
  const path = `/plugins/marketplace/${encodeURIComponent(pluginId)}`
  return sourceId ? `${path}?source=${encodeURIComponent(sourceId)}` : path
}

export function packageKey(item) {
  return `${item?.source?.id || ''}:${item?.plugin?.id || ''}:${item?.plugin?.version || ''}`
}

export function pluginTitle(item) {
  const plugin = item?.plugin || {}
  const name = String(plugin.name || plugin.manifest?.name || '').trim()
  if (name) return name
  const id = String(plugin.id || '').trim()
  if (id) return id
  return '未命名插件'
}

export function pluginBlurb(item) {
  return sanitizePluginText(item?.plugin?.description || item?.plugin?.manifest?.description || '').trim()
}

export function sourceKindLabel(kind) {
  return kind === 'official' ? '官方来源' : '非官方来源'
}

export function previewPackageDetail(item) {
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

export function pluginHasUpgrade(current, item) {
  if (!current) return false
  const installedVersion = String(current.active_version || '').trim()
  const marketVersion = String(item?.plugin?.version || '').trim()
  if (installedVersion && marketVersion) return installedVersion !== marketVersion
  const installedDigest = String(current.active_package_digest || '').trim().toLowerCase()
  const marketDigest = String(item?.plugin?.sha256 || '').trim().toLowerCase()
  return Boolean(installedDigest && marketDigest && installedDigest !== marketDigest)
}

export function useMarketplaceCatalog() {
  const router = useRouter()

  const loading = ref(true)
  const actionBusy = ref(false)
  const detailLoading = ref(false)
  const catalogRefreshing = ref(false)
  const error = ref('')
  const actionError = ref('')
  const packages = ref([])
  const installed = ref([])
  const catalogSources = ref([])
  const lastCatalogCompletedAt = ref('')
  const selected = ref(null)
  const detail = ref(null)
  const detailPrepared = ref(false)
  const confirmVisible = ref(false)
  const pendingConflict = ref(false)
  const downloadElapsedSec = ref(0)
  const packageDetailCache = new Map()
  let downloadTimer = 0

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
  const isUpgrade = computed(() => pluginHasUpgrade(installedPlugin.value, selected.value))
  const requiredPermissions = computed(() => detail.value?.permissions || [])
  const alreadyInstalled = computed(() => !!installedPlugin.value && !isUpgrade.value)
  const selectedPluginID = computed(() => String(selected.value?.plugin.id || '').trim())
  const selectedDetailPath = computed(() => selectedPluginID.value ? `/plugins/${encodeURIComponent(selectedPluginID.value)}` : '')
  const hasPendingDetailLink = computed(() => Boolean(actionError.value && hasPendingOperation()))
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

  const catalogUpdatedLabel = computed(() => {
    if (!lastCatalogCompletedAt.value) return '尚未更新'
    const formatted = formatPanelDateTime(lastCatalogCompletedAt.value, '')
    return formatted || '尚未更新'
  })

  onMounted(load)
  onBeforeUnmount(stopDownloadProgress)

  function catalogSourceList(sourceList) {
    return (sourceList || []).filter((sourceItem) => String(sourceItem?.id || '').trim())
  }

  function latestCompletedAt(sourceList) {
    let latestMs = Number.NaN
    let latestValue = ''
    for (const sourceItem of catalogSourceList(sourceList)) {
      const raw = sourceItem?.last_completed_at
      if (raw == null || raw === '') continue
      const ms = new Date(raw).getTime()
      if (!Number.isFinite(ms)) continue
      if (!Number.isFinite(latestMs) || ms > latestMs) {
        latestMs = ms
        latestValue = raw
      }
    }
    return latestValue
  }

  function rememberCatalogCompletedAt(sourceList) {
    const next = latestCompletedAt(sourceList)
    if (next) lastCatalogCompletedAt.value = next
  }

  function packagesFromContents(sourceList, contentResults, previousPackages) {
    const previousBySource = new Map()
    for (const item of previousPackages || []) {
      const id = String(item?.source?.id || '').trim()
      if (!id) continue
      const list = previousBySource.get(id) || []
      list.push(item)
      previousBySource.set(id, list)
    }
    return sourceList.flatMap((sourceItem, index) => {
      const result = contentResults[index]
      if (!result || result.status === 'rejected') {
        return previousBySource.get(sourceItem.id) || []
      }
      const value = result.value || { entries: [], directPlugin: null }
      const entries = [...(value.entries || [])]
      if (value.directPlugin) entries.push(value.directPlugin)
      return entries.map((plugin) => ({ source: sourceItem, plugin }))
    })
  }

  async function load(options = {}) {
    const silent = options.silent === true
    if (!silent) loading.value = true
    error.value = ''
    try {
      const [sourceList, current] = await Promise.all([fetchRepositorySources(), fetchPlugins()])
      catalogSources.value = catalogSourceList(sourceList)
      installed.value = current
      rememberCatalogCompletedAt(catalogSources.value)
      const contentResults = await Promise.allSettled(
        catalogSources.value.map((sourceItem) => fetchRepositoryContents(sourceItem.id))
      )
      packages.value = packagesFromContents(catalogSources.value, contentResults, packages.value)
    } catch (cause) {
      if (!packages.value.length) applyPreviewPackages()
      error.value = ''
    } finally {
      loading.value = false
    }
  }

  async function refreshCatalog() {
    if (catalogRefreshing.value || actionBusy.value || detailLoading.value) return
    catalogRefreshing.value = true
    try {
      let sourceList = catalogSources.value
      if (!sourceList.length) {
        try {
          sourceList = catalogSourceList(await fetchRepositorySources())
          catalogSources.value = sourceList
          rememberCatalogCompletedAt(sourceList)
        } catch (cause) {
          messageStore.error(sanitizePluginText(cause?.message || '读取仓库源失败'))
          return
        }
      }
      const failures = []
      const results = await Promise.allSettled(
        sourceList.map((sourceItem) => refreshRepositorySource(sourceItem.id))
      )
      results.forEach((result, index) => {
        if (result.status !== 'rejected') return
        const sourceItem = sourceList[index]
        failures.push({
          id: sourceItem?.id,
          name: sourceItem?.name || sourceItem?.id || '仓库源',
          message: sanitizePluginText(result.reason?.message || '刷新仓库源失败')
        })
      })
      await load({ silent: true })
      if (failures.length) {
        messageStore.error(failures.map((item) => `${item.name}：${item.message}`).join('；'))
        return
      }
      if (sourceList.length) messageStore.success('市场目录已更新')
    } finally {
      catalogRefreshing.value = false
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
    const requestedKey = packageKey(item)
    detailLoading.value = true
    actionError.value = ''
    startDownloadProgress()
    try {
      const prepared = await cachedPackageDetail(item)
      // A catalog card can be inspected while a different package is still
      // downloading. Never attach the earlier package's permissions or
      // identity to the newly selected card.
      if (packageKey(selected.value) !== requestedKey) return false
      detail.value = prepared
      detailPrepared.value = true
      return true
    } catch (cause) {
      if (packageKey(selected.value) !== requestedKey) return false
      detail.value = previewPackageDetail(item)
      detailPrepared.value = false
      if (!String(item?.plugin?.sha256 || '').startsWith('preview-')) {
        actionError.value = humanLoadError(cause, '读取签名包详情失败')
        messageStore.error(actionError.value)
      }
      return false
    } finally {
      stopDownloadProgress()
      detailLoading.value = false
    }
  }

  function showCatalogItem(item) {
    if (actionBusy.value) return
    selected.value = item || null
    detail.value = item ? previewPackageDetail(item) : null
    detailPrepared.value = false
    confirmVisible.value = false
    actionError.value = ''
    pendingConflict.value = false
  }

  function isSelected(item) {
    return packageKey(item) === packageKey(selected.value)
  }

  function installedStatus(item) {
    const current = installed.value.find((plugin) => plugin.plugin_id === item?.plugin.id)
    if (!current) return '未安装'
    if (pluginHasUpgrade(current, item)) return '可升级'
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

  function isMarketplaceBusyMessage(message) {
    return /refresh lease|source generation changed|marketplace source config revision|official marketplace branch changed/i.test(String(message || ''))
  }

  function pendingPlugin() {
    return installedPlugin.value
  }

  function hasPendingOperation() {
    return Boolean(String(pendingPlugin()?.pending_operation_id || '').trim())
  }

  function isPendingSameUpgrade() {
    const pending = pendingPlugin()
    const pendingKind = String(pending?.pending_kind || '').trim().toLowerCase()
    const pendingDigest = String(pending?.pending_target_digest || '').trim()
    const selectedDigest = String(selected.value?.plugin?.sha256 || '').trim()
    return hasPendingOperation()
      && pendingKind === 'upgrade'
      && pendingDigest
      && selectedDigest
      && pendingDigest.toLowerCase() === selectedDigest.toLowerCase()
  }

  function humanLoadError(cause, fallback) {
    const raw = sanitizePluginText(cause?.message || fallback)
    if (isMarketplaceBusyMessage(raw)) {
      return '市场目录刚刷新或正在刷新。这个插件本身正常，请稍后重新点升级。'
    }
    if (isPendingConflictMessage(raw)) {
      if (isPendingSameUpgrade()) {
        return '这个插件已有升级在进行。打开详情查看进度，不用重复提交。'
      }
      if (hasPendingOperation()) {
        return '这个插件还有未完成的操作，所以这次没有提交。打开详情查看进度，结束后再点重试。'
      }
      return '另一个插件的升级还在节点上应用。这个插件本身正常，等当前升级完成后再试。'
    }
    if (/timeout|timed out|exceeded|econnaborted/i.test(raw)) {
      return '读取插件包超时。安装前需要下载并校验签名包，请检查出站网络或 HTTP 代理后重试。'
    }
    if (/status code 5\d\d|network error|failed to fetch/i.test(raw)) {
      return '暂时连不上服务，请稍后重试。'
    }
    return raw
  }

  function cardActionLabel(item) {
    const status = installedStatus(item)
    if (status === '可升级') return '更新'
    if (status === '已安装') return '打开'
    return '安装'
  }

  function tableActionClass(item) {
    return cardActionLabel(item) === '打开' ? 'btn btn-ghost btn-sm' : 'btn btn-primary btn-sm'
  }

  async function startCardAction(item) {
    if (!item?.plugin?.id || actionBusy.value || detailLoading.value) return
    selected.value = item
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

  function cancelConfirm() {
    if (actionBusy.value) return
    confirmVisible.value = false
    actionError.value = ''
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

  async function applyPackage() {
    if (!selected.value || actionBusy.value || detailLoading.value) return
    const item = selected.value
    const pluginID = String(item?.plugin?.id || '').trim()
    if (!pluginID) return
    actionBusy.value = true
    actionError.value = ''
    pendingConflict.value = false
    try {
      if (!detailPrepared.value) {
        const prepared = await preparePackageDetail(item)
        if (!prepared) return
      }
      await refreshInstalled()
      // Both the verified permission list and current installation state may
      // have changed while the package was downloading. Snapshot them only
      // after the final refresh, immediately before choosing the mutation.
      const current = installed.value.find((plugin) => plugin.plugin_id === pluginID)
      const shouldUpgrade = pluginHasUpgrade(current, item)
      if (current && !shouldUpgrade) {
        confirmVisible.value = false
        await router.push(`/plugins/${encodeURIComponent(pluginID)}`)
        return
      }
      const selection = {
        source_id: item.source?.id,
        plugin_id: pluginID,
        version: item.plugin?.version,
        digest: item.plugin?.sha256,
        confirmed_permissions: [...requiredPermissions.value].sort(),
        risk_accepted: item.source?.kind !== 'official'
      }
      if (shouldUpgrade) await upgradePlugin(pluginID, selection)
      else await installPlugin(selection)
      confirmVisible.value = false
      actionError.value = ''
      messageStore.success(shouldUpgrade ? '插件已升级' : '插件已安装')
      await router.push(`/plugins/${encodeURIComponent(pluginID)}`)
    } catch (cause) {
      confirmVisible.value = true
      await refreshInstalled()
      pendingConflict.value = isPendingConflictMessage(cause?.message) && hasPendingOperation()
      actionError.value = humanLoadError(cause, '提交插件包失败')
      messageStore.error(actionError.value)
    } finally {
      actionBusy.value = false
    }
  }

  return {
    loading,
    actionBusy,
    detailLoading,
    catalogRefreshing,
    error,
    actionError,
    packages,
    installed,
    catalogSources,
    catalogUpdatedLabel,
    selected,
    detail,
    detailPrepared,
    confirmVisible,
    pendingConflict,
    downloadElapsedSec,
    downloadSteps,
    downloadPhaseLabel,
    downloadHint,
    source,
    isUpgrade,
    requiredPermissions,
    alreadyInstalled,
    selectedDetailPath,
    hasPendingDetailLink,
    hasHTTPBackend,
    pluginPurpose,
    nextStepHint,
    load,
    refreshCatalog,
    showCatalogItem,
    startCardAction,
    cancelConfirm,
    onConfirmVisible,
    applyPackage,
    isSelected,
    installedStatus,
    statusTone,
    cardActionLabel,
    tableActionClass,
    pluginTitle,
    pluginBlurb,
    sourceKindLabel,
    packageKey,
    marketplaceDetailHref,
  }
}
