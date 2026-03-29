"use strict";

const { parentPort } = require("worker_threads");
const core = require("./storage-prisma-core");

const operations = {
  init: async ({ dataRoot }) => core.loadSnapshot(dataRoot),
  saveRegisteredAgents: async ({ dataRoot, agents }) => {
    await core.saveRegisteredAgents(dataRoot, agents);
    return null;
  },
  saveRulesForAgent: async ({ dataRoot, agentId, rules }) => {
    await core.saveRulesForAgent(dataRoot, agentId, rules);
    return null;
  },
  deleteRulesForAgent: async ({ dataRoot, agentId }) => {
    await core.deleteRulesForAgent(dataRoot, agentId);
    return null;
  },
  saveL4RulesForAgent: async ({ dataRoot, agentId, rules }) => {
    await core.saveL4RulesForAgent(dataRoot, agentId, rules);
    return null;
  },
  deleteL4RulesForAgent: async ({ dataRoot, agentId }) => {
    await core.deleteL4RulesForAgent(dataRoot, agentId);
    return null;
  },
  saveManagedCertificates: async ({ dataRoot, certs }) => {
    await core.saveManagedCertificates(dataRoot, certs);
    return null;
  },
  saveLocalAgentState: async ({ dataRoot, state }) => {
    await core.saveLocalAgentState(dataRoot, state);
    return null;
  },
  migrateFromJson: async ({ dataRoot, payload }) => core.migrateFromJsonPayload(dataRoot, payload),
};

function writeResponse(controlBuffer, dataBuffer, payload) {
  const control = new Int32Array(controlBuffer);
  const bytes = Buffer.from(JSON.stringify(payload), "utf8");
  const target = new Uint8Array(dataBuffer);

  if (bytes.length > target.byteLength) {
    const fallback = Buffer.from(JSON.stringify({
      ok: false,
      error: `Prisma worker response exceeded ${target.byteLength} bytes`,
    }), "utf8");
    target.fill(0);
    target.set(fallback);
    Atomics.store(control, 1, fallback.length);
    Atomics.store(control, 0, -1);
    Atomics.notify(control, 0, 1);
    return;
  }

  target.fill(0);
  target.set(bytes);
  Atomics.store(control, 1, bytes.length);
  Atomics.store(control, 0, payload.ok ? 1 : -1);
  Atomics.notify(control, 0, 1);
}

parentPort.on("message", async ({ request, control, data }) => {
  try {
    const operation = operations[request?.op];
    if (!operation) {
      throw new Error(`Unsupported Prisma storage operation: ${request?.op || "<missing>"}`);
    }
    const result = await operation(request);
    writeResponse(control, data, { ok: true, result });
  } catch (err) {
    writeResponse(control, data, {
      ok: false,
      error: String(err && err.message ? err.message : err),
      stack: err && err.stack ? err.stack : undefined,
    });
  }
});