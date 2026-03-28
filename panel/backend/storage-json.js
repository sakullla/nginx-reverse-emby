"use strict";

const fs = require("fs");
const path = require("path");

const LOCAL_AGENT_ID = process.env.MASTER_LOCAL_AGENT_ID || "local";

let dataRoot = null;

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

// --- Storage interface ---

function init(root) {
  dataRoot = root;
  fs.mkdirSync(dataRoot, { recursive: true });
  fs.mkdirSync(path.join(dataRoot, "agent_rules"), { recursive: true });
  fs.mkdirSync(path.join(dataRoot, "l4_agent_rules"), { recursive: true });
}

function loadRulesForAgent(agentId) {
  const rules = readJsonFile(getRuleFileForAgent(agentId), []);
  if (!Array.isArray(rules)) return [];
  return rules;
}

function saveRulesForAgent(agentId, rules) {
  writeJsonFile(getRuleFileForAgent(agentId), Array.isArray(rules) ? rules : []);
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
  return agents;
}

function saveRegisteredAgents(agents) {
  writeJsonFile(dataPath("agents.json"), Array.isArray(agents) ? agents : []);
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
  if (!state || typeof state !== "object") return {};
  return state;
}

function saveLocalAgentState(state) {
  writeJsonFile(dataPath("local_agent_state.json"), state || {});
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

  return Math.max(agentMax, localMax, certMax, 0) + 1;
}

/** Parse a revision value defensively — mirrors server.js normalizeRevision logic */
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
  getNextGlobalRevision,
  migrateFromJson,
  close,
};
