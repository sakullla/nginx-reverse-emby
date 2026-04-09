"use strict";

function ensureObject(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError(`${label} must be an object`);
  }
  return value;
}

function normalizeOptionalId(value, label) {
  if (value === undefined) {
    return undefined;
  }
  if (value === null) {
    return null;
  }
  const parsed = Number.parseInt(String(value), 10);
  if (!Number.isInteger(parsed) || parsed < 0) {
    throw new TypeError(`${label} must be a non-negative integer`);
  }
  return parsed;
}

function normalizeRequiredId(value, label) {
  if (value === undefined || value === null || value === "") {
    throw new TypeError(`${label} is required`);
  }
  const parsed = Number.parseInt(String(value), 10);
  if (!Number.isInteger(parsed) || parsed < 0) {
    throw new TypeError(`${label} must be a non-negative integer`);
  }
  return parsed;
}

function normalizeRequiredString(value, label) {
  const next = String(value || "").trim();
  if (!next) {
    throw new TypeError(`${label} is required`);
  }
  return next;
}

function normalizeStringList(value) {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((entry) => String(entry || "").trim())
    .filter(Boolean);
}

function normalizeTrustedCaCertificateIds(value) {
  if (!Array.isArray(value)) {
    return [];
  }
  const seen = new Set();
  const ids = [];
  for (const entry of value) {
    const parsed = Number.parseInt(String(entry), 10);
    if (!Number.isInteger(parsed) || parsed < 0 || seen.has(parsed)) {
      continue;
    }
    seen.add(parsed);
    ids.push(parsed);
  }
  return ids;
}

function normalizePinSet(value) {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.map((entry) => {
    const next = ensureObject(entry, "pin_set entry");
    return {
      type: normalizeRequiredString(next.type, "pin_set.type"),
      value: normalizeRequiredString(next.value, "pin_set.value"),
    };
  });
}

function normalizeRevision(value) {
  const parsed = Number.parseInt(String(value || 0), 10);
  return Number.isInteger(parsed) && parsed >= 0 ? parsed : 0;
}

function normalizeListenPort(value) {
  const parsed = Number.parseInt(String(value), 10);
  if (!Number.isInteger(parsed) || parsed < 1 || parsed > 65535) {
    throw new TypeError("listen_port must be an integer between 1 and 65535");
  }
  return parsed;
}

function normalizeBindHosts(value) {
  if (!Array.isArray(value)) {
    return [];
  }
  const seen = new Set();
  const normalized = [];
  for (const entry of value) {
    const host = String(entry || "").trim();
    if (!host || seen.has(host)) {
      continue;
    }
    seen.add(host);
    normalized.push(host);
  }
  return normalized;
}

function normalizeRelayListenerPayload(payload, options = {}) {
  const allowDraft = options && options.allowDraft === true;
  const next = ensureObject(payload, "relay listener payload");
  const listenPort = normalizeListenPort(next.listen_port);
  const bindHosts = normalizeBindHosts(next.bind_hosts);
  const legacyListenHost = String(next.listen_host || "").trim();
  const normalizedBindHosts =
    bindHosts.length > 0
      ? bindHosts
      : [legacyListenHost || "0.0.0.0"];
  const listenHost = normalizedBindHosts[0];
  const publicHost = String(next.public_host || "").trim() || listenHost;
  const publicPort =
    next.public_port === undefined || next.public_port === null || next.public_port === ""
      ? listenPort
      : normalizeListenPort(next.public_port);
  const normalized = {
    id: normalizeRequiredId(next.id, "id"),
    agent_id: normalizeRequiredString(next.agent_id, "agent_id"),
    name: normalizeRequiredString(next.name, "name"),
    bind_hosts: normalizedBindHosts,
    listen_host: listenHost,
    listen_port: listenPort,
    public_host: publicHost,
    public_port: publicPort,
    enabled: next.enabled !== false,
    certificate_id: normalizeOptionalId(next.certificate_id, "certificate_id"),
    tls_mode: String(next.tls_mode || "pin_or_ca").trim() || "pin_or_ca",
    pin_set: normalizePinSet(next.pin_set),
    trusted_ca_certificate_ids: normalizeTrustedCaCertificateIds(next.trusted_ca_certificate_ids),
    allow_self_signed: !!next.allow_self_signed,
    tags: normalizeStringList(next.tags),
    revision: normalizeRevision(next.revision),
  };
  const allowedTlsModes = new Set(["pin_only", "ca_only", "pin_or_ca", "pin_and_ca"]);

  if (!allowedTlsModes.has(normalized.tls_mode)) {
    throw new TypeError("tls_mode must be pin_only, ca_only, pin_or_ca, or pin_and_ca");
  }

  if (!allowDraft && normalized.enabled && normalized.certificate_id == null) {
    throw new TypeError("certificate_id is required when relay listener is enabled");
  }

  if (!allowDraft && normalized.enabled) {
    if (normalized.tls_mode === "pin_and_ca") {
      if (normalized.pin_set.length === 0 || normalized.trusted_ca_certificate_ids.length === 0) {
        throw new TypeError("pin_and_ca requires both pin_set and trusted_ca_certificate_ids");
      }
    } else if (normalized.tls_mode === "pin_only") {
      if (normalized.pin_set.length === 0) {
        throw new TypeError("pin_only requires pin_set");
      }
    } else if (normalized.tls_mode === "ca_only") {
      if (normalized.trusted_ca_certificate_ids.length === 0) {
        throw new TypeError("ca_only requires trusted_ca_certificate_ids");
      }
    } else if (
      normalized.pin_set.length === 0 &&
      normalized.trusted_ca_certificate_ids.length === 0
    ) {
      throw new TypeError("pin_set and trusted_ca_certificate_ids cannot both be empty");
    }
  }

  return normalized;
}

function normalizeRelayListenerDraft(payload) {
  return normalizeRelayListenerPayload(payload, { allowDraft: true });
}

module.exports = {
  normalizeRelayListenerDraft,
  normalizeRelayListenerPayload,
};
