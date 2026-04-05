"use strict";

function normalizeHeaderName(value) {
  const name = String(value || "").trim();
  if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name)) {
    throw new Error("custom header name is invalid");
  }
  return name;
}

function normalizeHeaderValue(value) {
  const normalized = String(value ?? "");
  if (/[\u0000-\u001F\u007F]/.test(normalized)) {
    throw new Error("custom header value contains control characters");
  }
  return normalized;
}

function normalizeCustomHeaders(input) {
  const seen = new Set();
  return (Array.isArray(input) ? input : []).map((item) => {
    const name = normalizeHeaderName(item?.name);
    const lowered = name.toLowerCase();
    if (lowered === "user-agent") {
      throw new Error("custom header User-Agent is reserved");
    }
    if (seen.has(lowered)) {
      throw new Error(`duplicate custom header: ${name}`);
    }
    seen.add(lowered);
    return { name, value: normalizeHeaderValue(item?.value) };
  });
}

function normalizeRuleRequestHeaders(body = {}, fallback = {}) {
  return {
    pass_proxy_headers:
      body.pass_proxy_headers !== undefined
        ? !!body.pass_proxy_headers
        : fallback.pass_proxy_headers !== false,
    user_agent:
      body.user_agent !== undefined
        ? normalizeHeaderValue(body.user_agent).trim()
        : normalizeHeaderValue(fallback.user_agent || "").trim(),
    custom_headers:
      body.custom_headers !== undefined
        ? normalizeCustomHeaders(body.custom_headers)
        : normalizeCustomHeaders(fallback.custom_headers || []),
  };
}

module.exports = {
  normalizeRuleRequestHeaders,
  normalizeCustomHeaders,
  normalizeHeaderValue,
};
