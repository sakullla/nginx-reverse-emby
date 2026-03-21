import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api'

export const useRuleStore = defineStore('rules', () => {
  const systemInfo = ref({ role: 'master', default_agent_id: null, local_agent_enabled: false, managed_certificates_enabled: false, cf_token_configured: false })
  const agents = ref([])
  const selectedAgentId = ref('')
  const rules = ref([])
  const l4Rules = ref([])
  const certificates = ref([])
  const stats = ref({ totalRequests: '0', status: '未知' })
  const loading = ref(false)
  const error = ref(null)
  const statusMessage = ref(null)
  const searchQuery = ref('')
  const l4SearchQuery = ref('')
  const certSearchQuery = ref('')
  const selectedTags = ref([])
  const viewMode = ref(localStorage.getItem('rule_view_mode') || 'grid')

  const token = ref(localStorage.getItem('panel_token') || '')
  const isAuthenticated = ref(false)
  const isAuthReady = ref(false)

  // Global search state
  const globalSearchQuery = ref('')
  const globalSearchResults = ref([]) // [{ agentId, agentName, rules: [], l4Rules: [], certificates: [] }]
  const globalSearchLoading = ref(false)

  const selectedAgent = computed(() =>
    agents.value.find((agent) => agent.id === selectedAgentId.value) || null
  )
  const hasSelectedAgent = computed(() => !!selectedAgent.value)
  const hasRules = computed(() => rules.value.length > 0)
  const hasL4Rules = computed(() => l4Rules.value.length > 0)
  const filteredL4Rules = computed(() => {
    const raw = l4SearchQuery.value.trim()
    if (!raw) return [...l4Rules.value]
    const idMatch = raw.match(/^#id=(\S+)$/)
    if (idMatch) {
      return l4Rules.value.filter((rule) => String(rule.id) === idMatch[1])
    }
    const query = raw.toLowerCase()
    return l4Rules.value.filter((rule) =>
      String(rule.name || '').toLowerCase().includes(query) ||
      String(rule.protocol || '').toLowerCase().includes(query) ||
      String(rule.listen_host || '').toLowerCase().includes(query) ||
      String(rule.upstream_host || '').toLowerCase().includes(query) ||
      String(rule.listen_port || '').includes(query) ||
      String(rule.upstream_port || '').includes(query) ||
      (rule.tags || []).some((tag) => String(tag).toLowerCase().includes(query))
    )
  })
  const filteredCertificates = computed(() => {
    const raw = certSearchQuery.value.trim()
    if (!raw) return [...certificates.value]
    const idMatch = raw.match(/^#id=(\S+)$/)
    if (idMatch) {
      return certificates.value.filter((c) => String(c.id) === idMatch[1])
    }
    const q = raw.toLowerCase()
    return certificates.value.filter(
      (c) =>
        c.domain.toLowerCase().includes(q) ||
        (c.tags || []).some((t) => t.toLowerCase().includes(q))
    )
  })

  const onlineAgentsCount = computed(() =>
    agents.value.filter((agent) => agent.status === 'online').length
  )

  const allTags = computed(() => {
    const tags = rules.value.flatMap((rule) => rule.tags || [])
    return [...new Set(tags)].sort()
  })

  const filteredRules = computed(() => {
    let result = rules.value

    if (selectedTags.value.length > 0) {
      result = result.filter((rule) =>
        selectedTags.value.some((tag) => rule.tags?.includes(tag))
      )
    }

    if (searchQuery.value) {
      const raw = searchQuery.value.trim()
      const idMatch = raw.match(/^#id=(\S+)$/)
      if (idMatch) {
        result = result.filter((rule) => String(rule.id) === idMatch[1])
      } else {
        const query = raw.toLowerCase()
        result = result.filter((rule) =>
          (rule.name || '').toLowerCase().includes(query) ||
          rule.frontend_url.toLowerCase().includes(query) ||
          rule.backend_url.toLowerCase().includes(query) ||
          (rule.tags || []).some((tag) => tag.toLowerCase().includes(query))
        )
      }
    }

    return result
  })

  function setSelectedAgent(agentId) {
    selectedAgentId.value = agentId || ''
    selectedTags.value = []
    searchQuery.value = ''
  }

  async function checkAuth() {
    if (!token.value) {
      isAuthenticated.value = false
      isAuthReady.value = true
      return
    }

    try {
      const ok = await api.verifyToken(token.value)
      isAuthenticated.value = ok
      if (!ok) {
        token.value = ''
        localStorage.removeItem('panel_token')
        showError('登录令牌已过期，请重新登录')
      }
    } catch (err) {
      isAuthenticated.value = false
      token.value = ''
      localStorage.removeItem('panel_token')
      if (err.message.includes('401')) {
        showError('会话已过期，请重新登录')
      }
    } finally {
      isAuthReady.value = true
    }
  }

  async function login(inputToken) {
    loading.value = true
    try {
      const ok = await api.verifyToken(inputToken)
      if (!ok) {
        showError('Token \u65e0\u6548\u6216\u8fde\u63a5\u5931\u8d25')
        throw new Error('invalid token')
      }

      token.value = inputToken
      isAuthenticated.value = true
      localStorage.setItem('panel_token', inputToken)
      showSuccess('\u767b\u5f55\u6210\u529f')
    } catch (err) {
      if (!String(err?.message || '').includes('invalid token')) {
        showError('Token \u65e0\u6548\u6216\u8fde\u63a5\u5931\u8d25')
      }
      throw err
    } finally {
      loading.value = false
    }
  }

  function logout() {
    token.value = ''
    isAuthenticated.value = false
    localStorage.removeItem('panel_token')
    rules.value = []
    agents.value = []
    setSelectedAgent('')
    showInfo('已退出登录')
  }

  async function initialize() {
    if (!isAuthenticated.value) return
    loading.value = true
    error.value = null

    try {
      systemInfo.value = await api.fetchSystemInfo()
      await loadAgents()
      await loadSelectedAgentData()
    } catch (err) {
      error.value = err.message
      showError(err.message)
    } finally {
      loading.value = false
    }
  }

  async function loadAgents() {
    const agentList = await api.fetchAgents()
    agents.value = agentList

    if (!agents.value.length) {
      setSelectedAgent('')
      rules.value = []
      stats.value = { totalRequests: '0', status: '暂无节点' }
      return agents.value
    }

    const hasCurrent = agents.value.some((agent) => agent.id === selectedAgentId.value)
    if (!hasCurrent) {
      setSelectedAgent(systemInfo.value.default_agent_id || agents.value[0].id)
    }

    return agents.value
  }

  async function refreshClusterStatus() {
    if (!isAuthenticated.value) return
    try {
      await loadAgents()
      if (selectedAgentId.value) {
        await loadStats()
      }
    } catch (err) {
      console.error('刷新集群状态失败:', err)
    }
  }

  async function loadStats() {
    if (!selectedAgentId.value) {
      stats.value = { totalRequests: '0', status: '未选择节点' }
      return
    }
    try {
      stats.value = await api.fetchAgentStats(selectedAgentId.value)
    } catch (err) {
      stats.value = { totalRequests: '0', status: '获取失败' }
      throw err
    }
  }

  async function loadRules() {
    if (!isAuthenticated.value || !selectedAgentId.value) {
      rules.value = []
      return
    }
    const rulesData = await api.fetchRules(selectedAgentId.value)
    rules.value = rulesData
  }

  async function loadL4Rules() {
    if (!selectedAgentId.value) { l4Rules.value = []; return }
    try {
      l4Rules.value = await api.fetchL4Rules(selectedAgentId.value)
    } catch (err) { l4Rules.value = [] }
  }

  async function loadCertificates() {
    if (!selectedAgentId.value) { certificates.value = []; return }
    try {
      certificates.value = await api.fetchCertificates(selectedAgentId.value)
    } catch (err) { certificates.value = [] }
  }

  async function loadSelectedAgentData() {
    if (!selectedAgentId.value) {
      rules.value = []
      l4Rules.value = []
      stats.value = { totalRequests: '0', status: '暂无节点' }
      return
    }

    loading.value = true
    error.value = null
    try {
      await Promise.all([loadRules(), loadL4Rules(), loadStats(), loadCertificates()])
    } catch (err) {
      error.value = err.message
      showError(err.message)
    } finally {
      loading.value = false
    }
  }

  async function selectAgent(agentId) {
    setSelectedAgent(agentId)
    await loadSelectedAgentData()
  }

  function ensureAgentSelected() {
    if (!selectedAgentId.value) {
      throw new Error('请先选择一个 Agent 节点')
    }
  }

  async function addRule(
    frontend_url,
    backend_url,
    tags = [],
    enabled = true,
    proxy_redirect = true
  ) {
    ensureAgentSelected()
    loading.value = true
    error.value = null
    try {
      const newRule = await api.createRule(
        selectedAgentId.value,
        frontend_url,
        backend_url,
        tags,
        enabled,
        proxy_redirect
      )
      await loadSelectedAgentData()
      showSuccess('规则已新增')
      return newRule
    } catch (err) {
      error.value = err.message
      showError(err.message)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function modifyRule(
    id,
    frontend_url,
    backend_url,
    tags,
    enabled,
    proxy_redirect
  ) {
    ensureAgentSelected()
    loading.value = true
    error.value = null
    try {
      await api.updateRule(
        selectedAgentId.value,
        id,
        frontend_url,
        backend_url,
        tags,
        enabled,
        proxy_redirect
      )
      await loadSelectedAgentData()
      showSuccess(`规则 ${id} 已更新`)
    } catch (err) {
      error.value = err.message
      showError(err.message)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function removeRule(id) {
    ensureAgentSelected()
    loading.value = true
    error.value = null
    try {
      await api.deleteRule(selectedAgentId.value, id)
      await loadSelectedAgentData()
      showSuccess(`规则 ${id} 已删除`)
    } catch (err) {
      error.value = err.message
      showError(err.message)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function applyNginxConfig() {
    ensureAgentSelected()
    loading.value = true
    error.value = null
    try {
      const result = await api.applyConfig(selectedAgentId.value)
      const name = selectedAgent.value?.name || selectedAgentId.value
      if (String(result?.message || '').includes('heartbeat')) {
        showInfo(`已向节点 ${name} 下发配置，等待 Agent 心跳应用`)
      } else {
        showSuccess(`节点 ${name} 配置已应用`)
      }
      await Promise.all([loadAgents(), loadStats()])
    } catch (err) {
      error.value = err.message
      showError(err.message)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function toggleRule(id, enabled) {
    ensureAgentSelected()
    const rule = rules.value.find((r) => r.id === id)
    if (!rule) {
      throw new Error('规则不存在')
    }
    loading.value = true
    error.value = null
    try {
      await api.updateRule(
        selectedAgentId.value,
        id,
        rule.frontend_url,
        rule.backend_url,
        rule.tags || [],
        enabled,
        rule.proxy_redirect !== false
      )
      await loadSelectedAgentData()
      showSuccess(`规则 ${id} 已${enabled ? '启用' : '停用'}`)
    } catch (err) {
      error.value = err.message
      showError(err.message)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function removeAgent(agentId) {
    loading.value = true
    error.value = null
    try {
      const removed = await api.deleteAgent(agentId)
      await loadAgents()
      await loadSelectedAgentData()
      showSuccess(`节点 ${removed?.name || agentId} 已移除`)
      return removed
    } catch (err) {
      error.value = err.message
      showError(err.message)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function renameAgent(agentId, newName) {
    loading.value = true
    error.value = null
    try {
      const updated = await api.renameAgent(agentId, newName)
      await loadAgents()
      showSuccess(`节点已重命名为 ${newName}`)
      return updated
    } catch (err) {
      error.value = err.message
      showError(err.message)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function performGlobalSearch(query) {
    if (!query.trim()) {
      globalSearchResults.value = []
      return
    }
    globalSearchLoading.value = true
    try {
      const agentIds = agents.value.map((a) => a.id)
      const [allAgentRules, allAgentL4Rules, allAgentCerts] = await Promise.all([
        api.fetchAllAgentsRules(agentIds),
        api.fetchAllAgentsL4Rules(agentIds),
        api.fetchAllAgentsCertificates(agentIds)
      ])
      const raw = query.trim()
      const idMatch = raw.match(/^#id=(\S+)$/)
      const q = raw.toLowerCase()

      // Search HTTP rules
      const matchedHttpRules = allAgentRules
        .map(({ agentId, rules: agentRules }) => {
          const agent = agents.value.find((a) => a.id === agentId)
          const matched = agentRules.filter(
            (r) =>
              (r.name || '').toLowerCase().includes(q) ||
              r.frontend_url.toLowerCase().includes(q) ||
              r.backend_url.toLowerCase().includes(q) ||
              (r.tags || []).some((t) => t.toLowerCase().includes(q))
          )
          return { agentId, agentName: agent?.name || agentId, rules: matched }
        })
        .filter((g) => g.rules.length > 0)

      // Search L4 rules
      const matchedL4Rules = allAgentL4Rules
        .map(({ agentId, l4Rules: agentRules }) => {
          const agent = agents.value.find((a) => a.id === agentId)
          const matched = agentRules.filter(
            (r) =>
              (r.name || '').toLowerCase().includes(q) ||
              (r.protocol || '').toLowerCase().includes(q) ||
              (r.listen_host || '').toLowerCase().includes(q) ||
              (r.upstream_host || '').toLowerCase().includes(q) ||
              String(r.listen_port || '').includes(q) ||
              String(r.upstream_port || '').includes(q) ||
              (r.tags || []).some((t) => t.toLowerCase().includes(q))
          )
          return { agentId, agentName: agent?.name || agentId, l4Rules: matched }
        })
        .filter((g) => g.l4Rules.length > 0)

      // Search certificates per-agent
      const matchedCerts = allAgentCerts.map(({ agentId, certificates: agentCerts }) => {
        const agent = agents.value.find((a) => a.id === agentId)
        const matched = agentCerts.filter((c) => {
          if (idMatch) return String(c.id) === idMatch[1]
          return (
            c.domain.toLowerCase().includes(q) ||
            (c.tags || []).some((t) => t.toLowerCase().includes(q))
          )
        })
        return { agentId, agentName: agent?.name || agentId, certificates: matched }
      }).filter((g) => g.certificates.length > 0)

      // Combine all results into per-agent groups
      globalSearchResults.value = matchedHttpRules.map(g => ({
        agentId: g.agentId,
        agentName: g.agentName,
        rules: g.rules,
        l4Rules: matchedL4Rules.find(l => l.agentId === g.agentId)?.l4Rules || [],
        certificates: matchedCerts.find(c => c.agentId === g.agentId)?.certificates || []
      }))

      // Add L4-only / cert-only results
      ;[...matchedL4Rules, ...matchedCerts].forEach(item => {
        if (!globalSearchResults.value.some(g => g.agentId === item.agentId)) {
          globalSearchResults.value.push({
            agentId: item.agentId,
            agentName: item.agentName,
            rules: [],
            l4Rules: matchedL4Rules.find(l => l.agentId === item.agentId)?.l4Rules || [],
            certificates: matchedCerts.find(c => c.agentId === item.agentId)?.certificates || []
          })
        }
      })
    } catch (err) {
      showError('全局搜索失败: ' + err.message)
    } finally {
      globalSearchLoading.value = false
    }
  }

  function showSuccess(message) {
    statusMessage.value = { type: 'success', text: message }
    setTimeout(() => {
      statusMessage.value = null
    }, 5000)
  }

  function showError(message) {
    statusMessage.value = { type: 'error', text: message }
    setTimeout(() => {
      statusMessage.value = null
    }, 8000)
  }

  function showInfo(message) {
    statusMessage.value = { type: 'info', text: message }
    setTimeout(() => {
      statusMessage.value = null
    }, 5000)
  }

  function clearStatus() {
    statusMessage.value = null
  }

  function toggleViewMode() {
    viewMode.value = viewMode.value === 'grid' ? 'list' : 'grid'
    localStorage.setItem('rule_view_mode', viewMode.value)
  }

  // L4 Rules
  async function addL4Rule(payload) {
    ensureAgentSelected()
    loading.value = true
    try {
      await api.createL4Rule(selectedAgentId.value, payload)
      await loadSelectedAgentData()
      showSuccess('L4 规则已新增')
    } catch (err) { showError(err.message); throw err }
    finally { loading.value = false }
  }

  async function modifyL4Rule(id, payload) {
    ensureAgentSelected()
    loading.value = true
    try {
      await api.updateL4Rule(selectedAgentId.value, id, payload)
      await loadSelectedAgentData()
      showSuccess(`L4 规则 ${id} 已更新`)
    } catch (err) { showError(err.message); throw err }
    finally { loading.value = false }
  }

  async function removeL4Rule(id) {
    ensureAgentSelected()
    loading.value = true
    try {
      await api.deleteL4Rule(selectedAgentId.value, id)
      await loadSelectedAgentData()
      showSuccess(`L4 规则 ${id} 已删除`)
    } catch (err) { showError(err.message); throw err }
    finally { loading.value = false }
  }

  async function toggleL4Rule(id, enabled) {
    ensureAgentSelected()
    const rule = l4Rules.value.find((r) => r.id === id)
    if (!rule) throw new Error('规则不存在')
    loading.value = true
    try {
      await api.updateL4Rule(selectedAgentId.value, id, { ...rule, enabled })
      await loadSelectedAgentData()
      showSuccess(`L4 规则 ${id} 已${enabled ? '启用' : '停用'}`)
    } catch (err) { showError(err.message); throw err }
    finally { loading.value = false }
  }

  // Certificates (per-agent)
  async function addCertificate(payload) {
    ensureAgentSelected()
    loading.value = true
    try {
      await api.createCertificate(selectedAgentId.value, payload)
      await loadCertificates()
      showSuccess('证书已新增')
    } catch (err) { showError(err.message); throw err }
    finally { loading.value = false }
  }

  async function modifyCertificate(id, payload) {
    ensureAgentSelected()
    loading.value = true
    try {
      await api.updateCertificate(selectedAgentId.value, id, payload)
      await loadCertificates()
      showSuccess(`证书 ${id} 已更新`)
    } catch (err) { showError(err.message); throw err }
    finally { loading.value = false }
  }

  async function removeCertificate(id) {
    ensureAgentSelected()
    loading.value = true
    try {
      await api.deleteCertificate(selectedAgentId.value, id)
      await loadCertificates()
      showSuccess(`证书 ${id} 已删除`)
    } catch (err) { showError(err.message); throw err }
    finally { loading.value = false }
  }

  async function syncCertificate(id) {
    ensureAgentSelected()
    loading.value = true
    try {
      await api.issueCertificate(selectedAgentId.value, id)
      await loadCertificates()
      showSuccess('证书已签发/同步')
    } catch (err) { showError(err.message); throw err }
    finally { loading.value = false }
  }

  return {
    systemInfo,
    agents,
    selectedAgentId,
    selectedAgent,
    hasSelectedAgent,
    onlineAgentsCount,
    rules,
    stats,
    searchQuery,
    l4SearchQuery,
    certSearchQuery,
    selectedTags,
    allTags,
    viewMode,
    filteredRules,
    filteredL4Rules,
    filteredCertificates,
    loading,
    error,
    statusMessage,
    hasRules,
    hasL4Rules,
    l4Rules,
    certificates,
    isAuthenticated,
    isAuthReady,
    token,
    globalSearchQuery,
    globalSearchResults,
    globalSearchLoading,
    checkAuth,
    login,
    logout,
    initialize,
    loadAgents,
    refreshClusterStatus,
    loadRules,
    loadL4Rules,
    loadCertificates,
    loadStats,
    loadSelectedAgentData,
    selectAgent,
    addRule,
    modifyRule,
    removeRule,
    applyNginxConfig,
    toggleRule,
    removeAgent,
    renameAgent,
    performGlobalSearch,
    showSuccess,
    showError,
    showInfo,
    clearStatus,
    toggleViewMode,
    addL4Rule,
    modifyL4Rule,
    removeL4Rule,
    toggleL4Rule,
    addCertificate,
    modifyCertificate,
    removeCertificate,
    syncCertificate
  }
})
