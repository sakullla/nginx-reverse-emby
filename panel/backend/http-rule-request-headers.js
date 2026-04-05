"use strict";

function normalizeHeaderName(value) {
  const name = String(value || "").trim();
  if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name)) {
    throw new Error("custom header name is invalid");
  }
  return name;
}

function normalizeHeaderValue(value) {
  if (value === undefined || value === null) {
    return "";
  }
  if (typeof value !== "string") {
    throw new Error("custom header value must be a string");
  }
  const normalized = value;
  if (/[\u0000-\u001F\u007F]/.test(normalized)) {
    throw new Error("custom header value contains control characters");
  }
  return normalized;
}

function normalizeCustomHeaders(input, options = {}) {
  const rejectNullValues = options.rejectNullValues === true;
  if (input === undefined) return [];
  if (!Array.isArray(input)) {
    throw new Error("custom_headers must be an array");
  }
  const seen = new Set();
  return input.map((item) => {
    const name = normalizeHeaderName(item?.name);
    const lowered = name.toLowerCase();
    if (lowered === "user-agent") {
      throw new Error("custom header User-Agent is reserved");
    }
    if (seen.has(lowered)) {
      throw new Error(`duplicate custom header: ${name}`);
    }
    seen.add(lowered);
    if (rejectNullValues && item && item.value === null) {
      throw new Error("custom header value must be a string");
    }
    return { name, value: normalizeHeaderValue(item?.value) };
  });
}

function normalizeRuleRequestHeaders(body = {}, fallback = {}) {
  let passProxyHeaders;
  if (body.pass_proxy_headers !== undefined) {
    if (typeof body.pass_proxy_headers !== "boolean") {
      throw new Error("pass_proxy_headers must be a boolean");
    }
    passProxyHeaders = body.pass_proxy_headers;
  } else {
    passProxyHeaders = fallback.pass_proxy_headers !== false;
  }

  if (body.user_agent !== undefined && typeof body.user_agent !== "string") {
    throw new Error("user_agent must be a string");
  }

  return {
    pass_proxy_headers: passProxyHeaders,
    user_agent:
      body.user_agent !== undefined
        ? normalizeHeaderValue(body.user_agent).trim()
        : normalizeHeaderValue(fallback.user_agent || "").trim(),
    custom_headers:
      body.custom_headers !== undefined
        ? normalizeCustomHeaders(body.custom_headers, { rejectNullValues: true })
        : normalizeCustomHeaders(fallback.custom_headers || []),
  };
}

module.exports = {
  normalizeRuleRequestHeaders,
  normalizeCustomHeaders,
  normalizeHeaderValue,
};
