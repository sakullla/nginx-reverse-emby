"use strict";

const fs = require("fs");
const path = require("path");
const { normalizeCustomHeaders } = require("./http-rule-request-headers");
const { normalizeRelayListenerPayload } = require("./relay-listener-normalize");
const { normalizeVersionPolicyPayload } = require("./version-policy-normalize");

const LOCAL_AGENT_ID = process.env.MASTER_LOCAL_AGENT_ID || "local";

let dataRoot = null;

function sanitizeStoredCustomHeaders(value) {
  if (!Array.isArray(value)) {
    return [];
  }
  const seen = new Set();
  const sanitized = [];
  for (const header of value) {
    try {
      const normalized = normalizeCustomHeaders([header])[0];
      const key = normalized.name.toLowerCase();
      if (seen.has(key)) {
        continue;
      }
      seen.add(key);
      sanitized.push(normalized);
    } catch (_) {
      // drop malformed custom header rows at storage boundary
    }
  }
  return sanitized;
}

function sanitizeRuleForStorage(rule) {
  if (!rule || typeof rule !== "object") {
    return rule;
  }
  return {
    ...rule,
    custom_headers: sanitizeStoredCustomHeaders(rule.custom_headers),
  };
}

function normalizeAgentForStorage(agent) {
  if (!agent || typeof agent !== "object") {
    return agent;
  }
  return {
    ...agent,
    desired_version: String(agent.desired_version || ""),
  };
}

function normalizeLocalAgentStateForStorage(state) {
  const next = state && typeof state === "object" ? state : {};
  return {
    desired_revision: safeRevision(next.desired_revision),
    current_revision: safeRevision(next.current_revision),
    last_apply_revision: safeRevision(next.last_apply_revision),
    last_apply_status: next.last_apply_status || "success",
    last_apply_message: next.last_apply_message || "",
    desired_version: String(next.desired_version || ""),
  };
}

function sanitizeRelayListenersForStorage(listeners) {
  if (!Array.isArray(listeners)) {
    return [];
  }
  const sanitized = [];
  for (const listener of listeners) {
    try {
      sanitized.push(normalizeRelayListenerPayload(listener));
    } catch (_) {
      // drop malformed relay listeners at storage boundary
    }
  }
  return sanitized;
}

function normalizeRelayListenersForSave(agentId, listeners) {
  if (!Array.isArray(listeners)) {
    return [];
  }
  return listeners.map((listener) => normalizeRelayListenerPayload({
    ...(listener && typeof listener === "object" ? listener : {}),
    agent_id: String(agentId),
  }));
}

function sanitizeVersionPolicyForStorage(policy) {
  if (!policy || typeof policy !== "object") {
    return null;
  }
  try {
    return normalizeVersionPolicyPayload(policy);
  } catch (_) {
    return null;
  }
}

function sanitizeVersionPoliciesForStorage(policies) {
  if (!Array.isArray(policies)) {
    const single = sanitizeVersionPolicyForStorage(policies);
    return single ? [single] : [];
  }

  const sanitized = [];
  const seen = new Set();
  for (const policy of policies) {
    const normalized = sanitizeVersionPolicyForStorage(policy);
    if (!normalized || seen.has(normalized.id)) {
      continue;
    }
    seen.add(normalized.id);
    sanitized.push(normalized);
  }
  sanitized.sort((a, b) => String(a.id).localeCompare(String(b.id)));
  return sanitized;
}

function normalizeVersionPoliciesForSave(policies) {
  if (!Array.isArray(policies)) {
    return [];
  }

  const normalized = [];
  const seen = new Set();
  for (const policy of policies) {
    const next = normalizeVersionPolicyPayload(policy);
    if (seen.has(next.id)) {
      continue;
    }
    seen.add(next.id);
    normalized.push(next);
  }
  normalized.sort((a, b) => String(a.id).localeCompare(String(b.id)));
  return normalized;
}

function collectRelayListenerIdsExceptAgent(agentId) {
  const relayListenersDir = path.join(dataRoot, "relay_listeners");
  if (!fs.existsSync(relayListenersDir)) {
    return new Map();
  }

  const ids = new Map();
  for (const file of fs.readdirSync(relayListenersDir)) {
    if (!file.endsWith(".json")) {
      continue;
    }
    const ownerAgentId = file.replace(/\.json$/, "");
    if (ownerAgentId === String(agentId)) {
      continue;
    }
    const listeners = readJsonFile(path.join(relayListenersDir, file), []);
    for (const listener of sanitizeRelayListenersForStorage(listeners)) {
      ids.set(listener.id, ownerAgentId);
    }
  }
  return ids;
}

function assertRelayListenerIdsAreGloballyUnique(agentId, listeners) {
  const idsInOtherAgents = collectRelayListenerIdsExceptAgent(agentId);
  const seenInPayload = new Set();
  for (const listener of listeners) {
    if (seenInPayload.has(listener.id) || idsInOtherAgents.has(listener.id)) {
      throw new Error(`relay listener id ${listener.id} must be globally unique`);
    }
    seenInPayload.add(listener.id);
  }
}

// --- Internal helpers (mirrors server.js readJsonFile/writeJsonFile) ---

function readJsonFile(filePath, fallback) {
  try {
    if (!fs.existsSync(filePath)) return fallback;
    return JSON.parse(fs.readFileSync(filePath, "utf8"));
  } catch (err) {
    console.error(`Error reading ${filePath}:`, err);
    return fallback;
  }
}

function writeJsonFile(filePath, value) {
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  fs.writeFileSync(filePath, JSON.stringify(value, null, 2), "utf8");
}

// --- Path helpers ---

function dataPath(file) {
  return path.join(dataRoot, file);
}

function getRuleFileForAgent(agentId) {
  if (agentId === LOCAL_AGENT_ID) return dataPath("proxy_rules.json");
  return path.join(dataRoot, "agent_rules", `${agentId}.json`);
}

function getL4RuleFileForAgent(agentId) {
  return path.join(dataRoot, "l4_agent_rules", `${agentId}.json`);
}

function getRelayListenerFileForAgent(agentId) {
  return path.join(dataRoot, "relay_listeners", `${agentId}.json`);
}

function getVersionPoliciesFile() {
  return dataPath("version_policies.json");
}

function getLegacyVersionPolicyFile() {
  return dataPath("version_policy.json");
}

// --- Storage interface ---

function init(root) {
  dataRoot = root;
  fs.mkdirSync(dataRoot, { recursive: true });
  fs.mkdirSync(path.join(dataRoot, "agent_rules"), { recursive: true });
  fs.mkdirSync(path.join(dataRoot, "l4_agent_rules"), { recursive: true });
  fs.mkdirSync(path.join(dataRoot, "relay_listeners"), { recursive: true });
}

function loadRulesForAgent(agentId) {
  const rules = readJsonFile(getRuleFileForAgent(agentId), []);
  if (!Array.isArray(rules)) return [];
  return rules.map(sanitizeRuleForStorage);
}

function saveRulesForAgent(agentId, rules) {
  const nextRules = Array.isArray(rules) ? rules.map(sanitizeRuleForStorage) : [];
  writeJsonFile(getRuleFileForAgent(agentId), nextRules);
}

function deleteRulesForAgent(agentId) {
  const file = getRuleFileForAgent(agentId);
  if (agentId === LOCAL_AGENT_ID) {
    writeJsonFile(file, []);
    return;
  }
  if (fs.existsSync(file)) fs.unlinkSync(file);
}

function loadL4RulesForAgent(agentId) {
  const rules = readJsonFile(getL4RuleFileForAgent(agentId), []);
  if (!Array.isArray(rules)) return [];
  return rules;
}

function saveL4RulesForAgent(agentId, rules) {
  writeJsonFile(getL4RuleFileForAgent(agentId), Array.isArray(rules) ? rules : []);
}

function deleteL4RulesForAgent(agentId) {
  const file = getL4RuleFileForAgent(agentId);
  if (fs.existsSync(file)) fs.unlinkSync(file);
}

function loadRegisteredAgents() {
  const agents = readJsonFile(dataPath("agents.json"), []);
  if (!Array.isArray(agents)) return [];
  return agents.map(normalizeAgentForStorage);
}

function saveRegisteredAgents(agents) {
  const nextAgents = Array.isArray(agents) ? agents.map(normalizeAgentForStorage) : [];
  writeJsonFile(dataPath("agents.json"), nextAgents);
}

function loadManagedCertificates() {
  const certs = readJsonFile(dataPath("managed_certificates.json"), []);
  if (!Array.isArray(certs)) return [];
  return certs;
}

function saveManagedCertificates(certs) {
  writeJsonFile(dataPath("managed_certificates.json"), Array.isArray(certs) ? certs : []);
}

function loadLocalAgentState() {
  const state = readJsonFile(dataPath("local_agent_state.json"), {});
  if (!state || typeof state !== "object") return normalizeLocalAgentStateForStorage({});
  return normalizeLocalAgentStateForStorage(state);
}

function saveLocalAgentState(state) {
  writeJsonFile(dataPath("local_agent_state.json"), normalizeLocalAgentStateForStorage(state));
}

function loadRelayListenersForAgent(agentId) {
  const listeners = readJsonFile(getRelayListenerFileForAgent(agentId), []);
  return sanitizeRelayListenersForStorage(listeners);
}

function saveRelayListenersForAgent(agentId, listeners) {
  const nextListeners = normalizeRelayListenersForSave(agentId, listeners);
  assertRelayListenerIdsAreGloballyUnique(agentId, nextListeners);
  writeJsonFile(getRelayListenerFileForAgent(agentId), nextListeners);
}

function deleteRelayListenersForAgent(agentId) {
  const file = getRelayListenerFileForAgent(agentId);
  if (fs.existsSync(file)) fs.unlinkSync(file);
}

function loadVersionPolicies() {
  const versionPoliciesFile = getVersionPoliciesFile();
  if (fs.existsSync(versionPoliciesFile)) {
    return sanitizeVersionPoliciesForStorage(readJsonFile(versionPoliciesFile, []));
  }
  return sanitizeVersionPoliciesForStorage(readJsonFile(getLegacyVersionPolicyFile(), []));
}

function saveVersionPolicies(policies) {
  writeJsonFile(getVersionPoliciesFile(), normalizeVersionPoliciesForSave(policies));
}

function loadVersionPolicy() {
  return loadVersionPolicies()[0] || null;
}

function saveVersionPolicy(policy) {
  saveVersionPolicies([normalizeVersionPolicyPayload(policy)]);
}

function getNextGlobalRevision() {
  const agents = loadRegisteredAgents();
  const agentMax = agents.reduce(
    (max, agent) =>
      Math.max(
        max,
        safeRevision(agent?.desired_revision),
        safeRevision(agent?.current_revision),
        safeRevision(agent?.last_apply_revision),
      ),
    0,
  );

  const localState = loadLocalAgentState();
  const localMax = Math.max(
    safeRevision(localState?.desired_revision),
    safeRevision(localState?.current_revision),
    safeRevision(localState?.last_apply_revision),
  );

  const certs = loadManagedCertificates();
  const certMax = certs.reduce(
    (max, cert) => Math.max(max, safeRevision(cert?.revision)),
    0,
  );

  const relayListenersDir = path.join(dataRoot, "relay_listeners");
  const relayListenerFiles = fs.existsSync(relayListenersDir)
    ? fs.readdirSync(relayListenersDir).filter((file) => file.endsWith(".json"))
    : [];
  const relayMax = relayListenerFiles.reduce((max, file) => {
    const listeners = loadRelayListenersForAgent(file.replace(/\.json$/, ""));
    return listeners.reduce(
      (innerMax, listener) => Math.max(innerMax, safeRevision(listener?.revision)),
      max,
    );
  }, 0);

  return Math.max(agentMax, localMax, certMax, relayMax, 0) + 1;
}

/** Parse a revision value defensively - mirrors server.js normalizeRevision logic */
function safeRevision(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
}

function migrateFromJson() {
  return false;
}

function close() {
  // no-op for JSON backend
}

module.exports = {
  init,
  loadRegisteredAgents,
  saveRegisteredAgents,
  loadRulesForAgent,
  saveRulesForAgent,
  deleteRulesForAgent,
  loadL4RulesForAgent,
  saveL4RulesForAgent,
  deleteL4RulesForAgent,
  loadManagedCertificates,
  saveManagedCertificates,
  loadLocalAgentState,
  saveLocalAgentState,
  loadRelayListenersForAgent,
  saveRelayListenersForAgent,
  deleteRelayListenersForAgent,
  loadVersionPolicies,
  saveVersionPolicies,
  loadVersionPolicy,
  saveVersionPolicy,
  getNextGlobalRevision,
  migrateFromJson,
  close,
};
