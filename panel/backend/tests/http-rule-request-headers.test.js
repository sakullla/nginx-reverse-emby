"use strict";

const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const {
  normalizeRuleRequestHeaders,
} = require("../http-rule-request-headers");

describe("HTTP rule request header normalization", () => {
  it("fills defaults for pass_proxy_headers, user_agent, and custom_headers", () => {
    const rule = normalizeRuleRequestHeaders({}, {});

    assert.equal(rule.pass_proxy_headers, true);
    assert.equal(rule.user_agent, "");
    assert.deepEqual(rule.custom_headers, []);
  });

  it("normalizes and validates fallback user_agent values", () => {
    const rule = normalizeRuleRequestHeaders({}, { user_agent: "  Mozilla/5.0  " });
    assert.equal(rule.user_agent, "Mozilla/5.0");

    assert.throws(
      () => normalizeRuleRequestHeaders({}, { user_agent: "bad\u0007ua" }),
      /control characters/i,
    );
  });

  it("rejects custom User-Agent rows", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [{ name: "User-Agent", value: "bad" }],
          },
          {},
        ),
      /User-Agent/i,
    );
  });

  it("rejects case-insensitive duplicate custom header names", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [
              { name: "x-forwarded-for", value: "1.2.3.4" },
              { name: "X-Forwarded-For", value: "5.6.7.8" },
            ],
          },
          {},
        ),
      /duplicate/i,
    );
  });
});
