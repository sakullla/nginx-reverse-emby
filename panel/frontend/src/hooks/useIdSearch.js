/**
 * useIdSearch — 共享 #id= 解析与跨 agent 记录查找
 *
 * 为功能页和全局搜索提供：
 * - parseIdQuery(input): 识别 #id= 语法
 * - findRecordInAgents(allData, id, type): 跨 agent 数据结构中按 id 查找记录
 */

const ID_QUERY_REGEX = /^#id=(\S+)$/

/**
 * 解析输入是否为 #id= 查询
 * @param {string} input - 搜索框输入
 * @returns {{ isIdSearch: boolean, id: string } | null}
 */
export function parseIdQuery(input) {
  const raw = (input || '').trim()
  if (!raw) return null
  const match = raw.match(ID_QUERY_REGEX)
  if (!match) return null
  return { isIdSearch: true, id: match[1] }
}

/**
 * 在 fetchAllAgentsRules 返回结构中查找匹配 id 的规则
 * @param {Array<{agentId: string, rules: Array}>} allRulesData
 * @param {string} id
 * @returns {{ agentId: string, record: object, type: 'rule' } | null}
 */
function findRule(allRulesData, id) {
  for (const group of allRulesData || []) {
    for (const rule of group.rules || []) {
      if (String(rule.id) === id) {
        return { agentId: group.agentId, record: rule, type: 'rule' }
      }
    }
  }
  return null
}

/**
 * 在 fetchAllAgentsL4Rules 返回结构中查找匹配 id 的 L4 规则
 */
function findL4Rule(allL4Data, id) {
  for (const group of allL4Data || []) {
    for (const rule of group.l4Rules || []) {
      if (String(rule.id) === id) {
        return { agentId: group.agentId, record: rule, type: 'l4' }
      }
    }
  }
  return null
}

/**
 * 在 fetchAllAgentsCertificates 返回结构中查找匹配 id 的证书
 */
function findCertificate(allCertsData, id) {
  for (const group of allCertsData || []) {
    for (const cert of group.certificates || []) {
      if (String(cert.id) === id) {
        return { agentId: group.agentId, record: cert, type: 'cert' }
      }
    }
  }
  return null
}

/**
 * 在 fetchAllAgentsRelayListeners 返回结构中查找匹配 id 的 relay 监听器
 */
function findRelayListener(allRelayData, id) {
  for (const group of allRelayData || []) {
    for (const listener of group.listeners || []) {
      if (String(listener.id) === id) {
        return { agentId: group.agentId, record: listener, type: 'relay' }
      }
    }
  }
  return null
}

/**
 * 跨所有 agent 数据按 id 查找记录
 * @param {object} allData - 包含 rules/l4Rules/certificates/relayListeners 的数据
 * @param {string} id - 要查找的记录 id
 * @param {string} [type] - 限定类型：'rule'|'l4'|'cert'|'relay'，不传则搜全部
 * @returns {{ agentId: string, record: object, type: string } | null}
 */
export function findRecordInAgents(allData, id, type) {
  if (!allData || !id) return null

  if (!type || type === 'rule') {
    const found = findRule(allData.rules, id)
    if (found) return found
  }
  if (!type || type === 'l4') {
    const found = findL4Rule(allData.l4Rules, id)
    if (found) return found
  }
  if (!type || type === 'cert') {
    const found = findCertificate(allData.certificates, id)
    if (found) return found
  }
  if (!type || type === 'relay') {
    const found = findRelayListener(allData.relayListeners, id)
    if (found) return found
  }

  return null
}

/**
 * 跨所有 agent 数据按 id 查找所有匹配记录（用于多候选场景）
 * @param {object} allData
 * @param {string} id
 * @returns {Array<{agentId: string, record: object, type: string}>}
 */
export function findAllMatchesInAgents(allData, id) {
  if (!allData || !id) return []

  const matches = []

  for (const group of allData.rules || []) {
    for (const rule of group.rules || []) {
      if (String(rule.id) === id) {
        matches.push({ agentId: group.agentId, record: rule, type: 'rule' })
      }
    }
  }
  for (const group of allData.l4Rules || []) {
    for (const rule of group.l4Rules || []) {
      if (String(rule.id) === id) {
        matches.push({ agentId: group.agentId, record: rule, type: 'l4' })
      }
    }
  }
  for (const group of allData.certificates || []) {
    for (const cert of group.certificates || []) {
      if (String(cert.id) === id) {
        matches.push({ agentId: group.agentId, record: cert, type: 'cert' })
      }
    }
  }
  for (const group of allData.relayListeners || []) {
    for (const listener of group.listeners || []) {
      if (String(listener.id) === id) {
        matches.push({ agentId: group.agentId, record: listener, type: 'relay' })
      }
    }
  }

  return matches
}
