import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api'

export const useRuleStore = defineStore('rules', () => {
  const systemInfo = ref({ role: 'master', default_agent_id: null, local_agent_enabled: false })
  const agents = ref([])
  const selectedAgentId = ref(localStorage.getItem('selected_agent_id') || '')
  const rules = ref([])
  const stats = ref({ totalRequests: '0', status: '未知' })
  const loading = ref(false)
  const error = ref(null)
  const statusMessage = ref(null)
  const searchQuery = ref('')
  const selectedTags = ref([])
  const viewMode = ref(localStorage.getItem('rule_view_mode') || 'grid')

  const token = ref(localStorage.getItem('panel_token') || '')
  const isAuthenticated = ref(false)
  const isAuthReady = ref(false)

  const selectedAgent = computed(() =>
    agents.value.find((agent) => agent.id === selectedAgentId.value) || null
  )
  const hasSelectedAgent = computed(() => !!selectedAgent.value)
  const hasRules = computed(() => rules.value.length > 0)
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
      const query = searchQuery.value.toLowerCase()
      result = result.filter((rule) =>
        rule.frontend_url.toLowerCase().includes(query) ||
        rule.backend_url.toLowerCase().includes(query) ||
        String(rule.id).includes(query)
      )
    }

    return result
  })

  function setSelectedAgent(agentId) {
    selectedAgentId.value = agentId || ''
    if (selectedAgentId.value) {
      localStorage.setItem('selected_agent_id', selectedAgentId.value)
    } else {
      localStorage.removeItem('selected_agent_id')
    }
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

  async function loadSelectedAgentData() {
    if (!selectedAgentId.value) {
      rules.value = []
      stats.value = { totalRequests: '0', status: '暂无节点' }
      return
    }

    loading.value = true
    error.value = null
    try {
      await Promise.all([loadRules(), loadStats()])
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
    selectedTags,
    allTags,
    viewMode,
    filteredRules,
    loading,
    error,
    statusMessage,
    hasRules,
    isAuthenticated,
    isAuthReady,
    token,
    checkAuth,
    login,
    logout,
    initialize,
    loadAgents,
    refreshClusterStatus,
    loadRules,
    loadStats,
    loadSelectedAgentData,
    selectAgent,
    addRule,
    modifyRule,
    removeRule,
    applyNginxConfig,
    removeAgent,
    showSuccess,
    showError,
    showInfo,
    clearStatus,
    toggleViewMode
  }
})
