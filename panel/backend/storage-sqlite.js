"use strict";

const fs = require("fs");
const os = require("os");
const path = require("path");
const { Worker } = require("worker_threads");

const jsonStorage = require("./storage-json");

const WORKER_PATH = path.join(__dirname, "storage-prisma-worker.js");
const LOCAL_AGENT_ID = process.env.MASTER_LOCAL_AGENT_ID || "local";
const WORKER_TIMEOUT_MS = 30000;
const INITIAL_WORKER_BUFFER_BYTES = 256 * 1024;

let state = createEmptyState();
let worker = null;
let workerControlBuffer = null;
let workerDataBuffer = null;
let workerControlView = null;
let workerBufferBytes = 0;

function createEmptyState() {
  return {
    dataRoot: null,
    tempDir: null,
    agents: [],
    rulesByAgent: new Map(),
    l4RulesByAgent: new Map(),
    managedCertificates: [],
    localAgentState: defaultLocalAgentState(),
    meta: {},
  };
}

function defaultLocalAgentState() {
  return {
    desired_revision: 0,
    current_revision: 0,
    last_apply_revision: 0,
    last_apply_status: "success",
    last_apply_message: "",
  };
}

function clone(value) {
  return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
}

function safeRevision(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
}

function normalizeAgent(agent) {
  return {
    id: agent.id,
    name: agent.name,
    agent_url: agent.agent_url || "",
    agent_token: agent.agent_token || "",
    version: agent.version || "",
    tags: Array.isArray(agent.tags) ? clone(agent.tags) : [],
    capabilities: Array.isArray(agent.capabilities) ? clone(agent.capabilities) : [],
    mode: agent.mode || "pull",
    desired_revision: safeRevision(agent.desired_revision),
    current_revision: safeRevision(agent.current_revision),
    last_apply_revision: safeRevision(agent.last_apply_revision),
    last_apply_status: agent.last_apply_status || null,
    last_apply_message: agent.last_apply_message || "",
    last_reported_stats:
      agent.last_reported_stats !== undefined && agent.last_reported_stats !== null
        ? clone(agent.last_reported_stats)
        : null,
    last_seen_at: agent.last_seen_at || null,
    last_seen_ip: agent.last_seen_ip || null,
    created_at: agent.created_at || null,
    updated_at: agent.updated_at || null,
    error: agent.error || null,
    is_local: !!agent.is_local,
  };
}

function normalizeRule(agentId, rule) {
  return {
    id: Number(rule.id),
    agent_id: String(agentId),
    frontend_url: rule.frontend_url,
    backend_url: rule.backend_url,
    enabled: !!rule.enabled,
    tags: Array.isArray(rule.tags) ? clone(rule.tags) : [],
    proxy_redirect: !!rule.proxy_redirect,
    revision: safeRevision(rule.revision),
  };
}

function normalizeL4Rule(agentId, rule) {
  return {
    id: Number(rule.id),
    agent_id: String(agentId),
    name: rule.name || "",
    protocol: rule.protocol || "tcp",
    listen_host: rule.listen_host || "0.0.0.0",
    listen_port: Number(rule.listen_port),
    upstream_host: rule.upstream_host || "",
    upstream_port: Number(rule.upstream_port || 0),
    backends: Array.isArray(rule.backends) ? clone(rule.backends) : [],
    load_balancing:
      rule.load_balancing && typeof rule.load_balancing === "object"
        ? clone(rule.load_balancing)
        : {},
    tuning:
      rule.tuning && typeof rule.tuning === "object"
        ? clone(rule.tuning)
        : {},
    enabled: !!rule.enabled,
    tags: Array.isArray(rule.tags) ? clone(rule.tags) : [],
    revision: safeRevision(rule.revision),
  };
}

function normalizeManagedCertificate(cert) {
  return {
    id: Number(cert.id),
    domain: cert.domain,
    enabled: !!cert.enabled,
    scope: cert.scope || "domain",
    issuer_mode: cert.issuer_mode || "master_cf_dns",
    target_agent_ids: Array.isArray(cert.target_agent_ids) ? clone(cert.target_agent_ids) : [],
    status: cert.status || "pending",
    last_issue_at: cert.last_issue_at || null,
    last_error: cert.last_error || "",
    material_hash: cert.material_hash || "",
    agent_reports:
      cert.agent_reports && typeof cert.agent_reports === "object"
        ? clone(cert.agent_reports)
        : {},
    acme_info:
      cert.acme_info && typeof cert.acme_info === "object"
        ? clone(cert.acme_info)
        : {},
    tags: Array.isArray(cert.tags) ? clone(cert.tags) : [],
    revision: safeRevision(cert.revision),
  };
}

function normalizeLocalAgentState(stateValue) {
  const nextState = stateValue && typeof stateValue === "object" ? stateValue : {};
  return {
    desired_revision: safeRevision(nextState.desired_revision),
    current_revision: safeRevision(nextState.current_revision),
    last_apply_revision: safeRevision(nextState.last_apply_revision),
    last_apply_status: nextState.last_apply_status || "success",
    last_apply_message: nextState.last_apply_message || "",
  };
}

function ensureInitialized() {
  if (!state.dataRoot) {
    throw new Error("storage not initialized");
  }
}

function ensureWorker() {
  if (!worker) {
    worker = new Worker(WORKER_PATH);
    worker.unref();
    resizeWorkerBuffers(INITIAL_WORKER_BUFFER_BYTES);
  }
}

function resetWorkerBuffers() {
  workerControlBuffer = null;
  workerDataBuffer = null;
  workerControlView = null;
  workerBufferBytes = 0;
}

function resizeWorkerBuffers(nextSize) {
  workerControlBuffer = new SharedArrayBuffer(Int32Array.BYTES_PER_ELEMENT * 2);
  workerDataBuffer = new SharedArrayBuffer(nextSize);
  workerControlView = new Int32Array(workerControlBuffer);
  workerBufferBytes = nextSize;
}

function disposeWorker() {
  if (worker) {
    worker.terminate();
    worker = null;
  }
  resetWorkerBuffers();
}

function runWorker(op, payload = {}, attempt = 0) {
  ensureWorker();
  Atomics.store(workerControlView, 0, 0);
  Atomics.store(workerControlView, 1, 0);

  worker.postMessage({
    request: { op, ...payload },
    control: workerControlBuffer,
    data: workerDataBuffer,
  });

  const waitResult = Atomics.wait(workerControlView, 0, 0, WORKER_TIMEOUT_MS);
  if (waitResult === "timed-out") {
    disposeWorker();
    throw new Error(`Prisma storage worker timed out after ${WORKER_TIMEOUT_MS}ms`);
  }

  const length = Atomics.load(workerControlView, 1);
  const raw = Buffer.from(new Uint8Array(workerDataBuffer, 0, length)).toString("utf8");
  const parsed = raw ? JSON.parse(raw) : { ok: false, error: "Empty Prisma worker response" };

  if (
    !parsed.ok &&
    parsed.code === "RESPONSE_TOO_LARGE" &&
    Number.isInteger(parsed.requiredBufferBytes) &&
    parsed.requiredBufferBytes > workerBufferBytes &&
    attempt < 3
  ) {
    let nextSize = workerBufferBytes;
    while (nextSize < parsed.requiredBufferBytes) {
      nextSize *= 2;
    }
    resizeWorkerBuffers(nextSize);
    return runWorker(op, payload, attempt + 1);
  }

  if (!parsed.ok) {
    throw new Error(parsed.error || "Prisma storage worker failed");
  }

  return parsed.result;
}

function resolveDataRoot(dataRoot) {
  if (dataRoot === ":memory:") {
    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "panel-prisma-"));
    return { dataRoot: tempDir, tempDir };
  }
  return { dataRoot, tempDir: null };
}

function applySnapshot(snapshot) {
  state.agents = clone(snapshot?.agents || []);
  state.rulesByAgent = new Map(
    Object.entries(snapshot?.rulesByAgent || {}).map(([agentId, rules]) => [agentId, clone(rules || [])]),
  );
  state.l4RulesByAgent = new Map(
    Object.entries(snapshot?.l4RulesByAgent || {}).map(([agentId, rules]) => [agentId, clone(rules || [])]),
  );
  state.managedCertificates = clone(snapshot?.managedCertificates || []);
  state.localAgentState = clone(snapshot?.localAgentState || defaultLocalAgentState());
  state.meta = clone(snapshot?.meta || {});
}

function cleanupTempDir(tempDir) {
  if (!tempDir || !fs.existsSync(tempDir)) {
    return;
  }

  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      fs.rmSync(tempDir, { recursive: true, force: true });
      return;
    } catch (err) {
      if (attempt === 19) {
        throw err;
      }
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 50);
    }
  }
}

function init(dataRoot) {
  close();
  const resolved = resolveDataRoot(dataRoot);
  state.dataRoot = resolved.dataRoot;
  state.tempDir = resolved.tempDir;
  applySnapshot(runWorker("init", { dataRoot: state.dataRoot }));
}

function loadRegisteredAgents() {
  ensureInitialized();
  return clone(state.agents);
}

function saveRegisteredAgents(agents) {
  ensureInitialized();
  const nextAgents = Array.isArray(agents) ? clone(agents) : [];
  runWorker("saveRegisteredAgents", { dataRoot: state.dataRoot, agents: nextAgents });
  state.agents = nextAgents.map(normalizeAgent);
}

function loadRulesForAgent(agentId) {
  ensureInitialized();
  return clone(state.rulesByAgent.get(String(agentId)) || []);
}

function saveRulesForAgent(agentId, rules) {
  ensureInitialized();
  const key = String(agentId);
  const nextRules = Array.isArray(rules) ? clone(rules) : [];
  runWorker("saveRulesForAgent", { dataRoot: state.dataRoot, agentId: key, rules: nextRules });
  state.rulesByAgent.set(key, nextRules.map((rule) => normalizeRule(key, rule)));
}

function deleteRulesForAgent(agentId) {
  ensureInitialized();
  const key = String(agentId);
  runWorker("deleteRulesForAgent", { dataRoot: state.dataRoot, agentId: key });
  state.rulesByAgent.delete(key);
}

function loadL4RulesForAgent(agentId) {
  ensureInitialized();
  return clone(state.l4RulesByAgent.get(String(agentId)) || []);
}

function saveL4RulesForAgent(agentId, rules) {
  ensureInitialized();
  const key = String(agentId);
  const nextRules = Array.isArray(rules) ? clone(rules) : [];
  runWorker("saveL4RulesForAgent", { dataRoot: state.dataRoot, agentId: key, rules: nextRules });
  state.l4RulesByAgent.set(key, nextRules.map((rule) => normalizeL4Rule(key, rule)));
}

function deleteL4RulesForAgent(agentId) {
  ensureInitialized();
  const key = String(agentId);
  runWorker("deleteL4RulesForAgent", { dataRoot: state.dataRoot, agentId: key });
  state.l4RulesByAgent.delete(key);
}

function loadManagedCertificates() {
  ensureInitialized();
  return clone(state.managedCertificates);
}

function saveManagedCertificates(certs) {
  ensureInitialized();
  const nextCerts = Array.isArray(certs) ? clone(certs) : [];
  runWorker("saveManagedCertificates", { dataRoot: state.dataRoot, certs: nextCerts });
  state.managedCertificates = nextCerts.map(normalizeManagedCertificate);
}

function loadLocalAgentState() {
  ensureInitialized();
  return clone(state.localAgentState);
}

function saveLocalAgentState(localAgentState) {
  ensureInitialized();
  const nextState = localAgentState && typeof localAgentState === "object"
    ? { ...defaultLocalAgentState(), ...clone(localAgentState) }
    : defaultLocalAgentState();
  runWorker("saveLocalAgentState", { dataRoot: state.dataRoot, state: nextState });
  state.localAgentState = normalizeLocalAgentState(nextState);
}

function getNextGlobalRevision() {
  ensureInitialized();

  const agentMax = state.agents.reduce((max, agent) => Math.max(
    max,
    safeRevision(agent?.desired_revision),
    safeRevision(agent?.current_revision),
    safeRevision(agent?.last_apply_revision),
  ), 0);

  const localMax = Math.max(
    safeRevision(state.localAgentState?.desired_revision),
    safeRevision(state.localAgentState?.current_revision),
    safeRevision(state.localAgentState?.last_apply_revision),
  );

  const certMax = state.managedCertificates.reduce(
    (max, cert) => Math.max(max, safeRevision(cert?.revision)),
    0,
  );

  return Math.max(agentMax, localMax, certMax, 0) + 1;
}

function migrateFromJson() {
  ensureInitialized();
  if (state.meta.migrated_from_json) {
    return false;
  }

  const dataPath = (file) => path.join(state.dataRoot, file);
  const agentRulesDir = dataPath("agent_rules");
  const l4RulesDir = dataPath("l4_agent_rules");

  const hasAgentsFile = fs.existsSync(dataPath("agents.json"));
  const hasProxyRulesFile = fs.existsSync(dataPath("proxy_rules.json"));
  const hasManagedCertsFile = fs.existsSync(dataPath("managed_certificates.json"));
  const hasLocalStateFile = fs.existsSync(dataPath("local_agent_state.json"));
  const agentRuleFiles = fs.existsSync(agentRulesDir)
    ? fs.readdirSync(agentRulesDir).filter((file) => file.endsWith(".json"))
    : [];
  const l4RuleFiles = fs.existsSync(l4RulesDir)
    ? fs.readdirSync(l4RulesDir).filter((file) => file.endsWith(".json"))
    : [];

  const hasOldData =
    hasAgentsFile ||
    hasProxyRulesFile ||
    hasManagedCertsFile ||
    hasLocalStateFile ||
    agentRuleFiles.length > 0 ||
    l4RuleFiles.length > 0;

  if (!hasOldData) {
    return false;
  }

  jsonStorage.init(state.dataRoot);
  const agents = jsonStorage.loadRegisteredAgents();
  const managedCertificates = jsonStorage.loadManagedCertificates();
  const localAgentState = jsonStorage.loadLocalAgentState();

  const agentIdsFromJson = Array.isArray(agents) ? agents.map((agent) => agent.id) : [];
  const agentIdsFromRuleFiles = agentRuleFiles.map((file) => file.replace(/\.json$/, ""));
  const agentIdsFromL4Files = l4RuleFiles.map((file) => file.replace(/\.json$/, ""));
  const allAgentIds = [
    ...new Set([LOCAL_AGENT_ID, ...agentIdsFromJson, ...agentIdsFromRuleFiles, ...agentIdsFromL4Files]),
  ];

  const payload = {
    agents: Array.isArray(agents) ? agents : [],
    managedCertificates: Array.isArray(managedCertificates) ? managedCertificates : [],
    localAgentState: localAgentState && typeof localAgentState === "object"
      ? { ...defaultLocalAgentState(), ...localAgentState }
      : defaultLocalAgentState(),
    rulesByAgent: Object.fromEntries(allAgentIds.map((agentId) => [agentId, jsonStorage.loadRulesForAgent(agentId)])),
    l4RulesByAgent: Object.fromEntries(allAgentIds.map((agentId) => [agentId, jsonStorage.loadL4RulesForAgent(agentId)])),
  };

  const result = runWorker("migrateFromJson", { dataRoot: state.dataRoot, payload });
  applySnapshot(result.snapshot);
  return !!result.migrated;
}

function close() {
  const tempDir = state.tempDir;
  if (worker) {
    try {
      runWorker("close");
    } catch (_) {
      // ignore worker shutdown errors and force termination below
    }
  }
  disposeWorker();
  state = createEmptyState();
  cleanupTempDir(tempDir);
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
