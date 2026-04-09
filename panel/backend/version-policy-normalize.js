"use strict";

function ensureObject(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError(`${label} must be an object`);
  }
  return value;
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

function normalizePackages(value) {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.map((entry) => {
    const next = ensureObject(entry, "packages entry");
    return {
      platform: normalizeRequiredString(next.platform, "packages.platform"),
      url: normalizeRequiredString(next.url, "packages.url"),
      sha256: normalizeRequiredString(next.sha256, "packages.sha256"),
    };
  });
}

function normalizeVersionPolicyPayload(payload) {
  const next = ensureObject(payload, "version policy payload");
  return {
    id: String(next.id || "default").trim() || "default",
    channel: String(next.channel || "stable").trim() || "stable",
    desired_version: normalizeRequiredString(next.desired_version, "desired_version"),
    packages: normalizePackages(next.packages),
    tags: normalizeStringList(next.tags),
  };
}

module.exports = {
  normalizeVersionPolicyPayload,
};
