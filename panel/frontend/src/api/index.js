let implementationPromise

function loadImplementation() {
  if (!implementationPromise) {
    implementationPromise = import.meta.env.DEV
      ? import('./devRuntime.js')
      : import('./runtime.js')
  }
  return implementationPromise
}

function call(name, ...args) {
  return loadImplementation().then((implementation) => implementation[name](...args))
}

export const verifyToken = (...args) => call('verifyToken', ...args)
export const fetchSystemInfo = (...args) => call('fetchSystemInfo', ...args)
export const exportBackup = (...args) => call('exportBackup', ...args)
export const importBackup = (...args) => call('importBackup', ...args)
export const fetchAgents = (...args) => call('fetchAgents', ...args)
export const updateAgent = (...args) => call('updateAgent', ...args)
export const fetchAgentStats = (...args) => call('fetchAgentStats', ...args)
export const fetchRules = (...args) => call('fetchRules', ...args)
export const createRule = (...args) => call('createRule', ...args)
export const updateRule = (...args) => call('updateRule', ...args)
export const deleteRule = (...args) => call('deleteRule', ...args)
export const diagnoseRule = (...args) => call('diagnoseRule', ...args)
export const fetchAgentTask = (...args) => call('fetchAgentTask', ...args)
export const applyConfig = (...args) => call('applyConfig', ...args)
export const deleteAgent = (...args) => call('deleteAgent', ...args)
export const renameAgent = (...args) => call('renameAgent', ...args)
export const fetchAllAgentsRules = (...args) => call('fetchAllAgentsRules', ...args)
export const fetchAllAgentsL4Rules = (...args) => call('fetchAllAgentsL4Rules', ...args)
export const checkHealth = (...args) => call('checkHealth', ...args)
export const fetchL4Rules = (...args) => call('fetchL4Rules', ...args)
export const createL4Rule = (...args) => call('createL4Rule', ...args)
export const updateL4Rule = (...args) => call('updateL4Rule', ...args)
export const deleteL4Rule = (...args) => call('deleteL4Rule', ...args)
export const diagnoseL4Rule = (...args) => call('diagnoseL4Rule', ...args)
export const fetchCertificates = (...args) => call('fetchCertificates', ...args)
export const createCertificate = (...args) => call('createCertificate', ...args)
export const updateCertificate = (...args) => call('updateCertificate', ...args)
export const deleteCertificate = (...args) => call('deleteCertificate', ...args)
export const issueCertificate = (...args) => call('issueCertificate', ...args)
export const fetchAllAgentsCertificates = (...args) => call('fetchAllAgentsCertificates', ...args)
export const fetchAllAgentsRelayListeners = (...args) => call('fetchAllAgentsRelayListeners', ...args)
export const fetchRelayListeners = (...args) => call('fetchRelayListeners', ...args)
export const fetchAllRelayListeners = (...args) => call('fetchAllRelayListeners', ...args)
export const createRelayListener = (...args) => call('createRelayListener', ...args)
export const updateRelayListener = (...args) => call('updateRelayListener', ...args)
export const deleteRelayListener = (...args) => call('deleteRelayListener', ...args)
export const fetchVersionPolicies = (...args) => call('fetchVersionPolicies', ...args)
export const createVersionPolicy = (...args) => call('createVersionPolicy', ...args)
export const updateVersionPolicy = (...args) => call('updateVersionPolicy', ...args)
export const deleteVersionPolicy = (...args) => call('deleteVersionPolicy', ...args)
export const fetchClientPackages = (...args) => call('fetchClientPackages', ...args)
export const createClientPackage = (...args) => call('createClientPackage', ...args)
export const updateClientPackage = (...args) => call('updateClientPackage', ...args)
export const deleteClientPackage = (...args) => call('deleteClientPackage', ...args)
export const fetchLatestClientPackage = (...args) => call('fetchLatestClientPackage', ...args)
export const exportBackupSelective = (...args) => call('exportBackupSelective', ...args)
export const importBackupPreview = (...args) => call('importBackupPreview', ...args)
export const fetchBackupResourceCounts = (...args) => call('fetchBackupResourceCounts', ...args)
