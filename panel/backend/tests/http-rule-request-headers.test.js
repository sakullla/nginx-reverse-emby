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

  it("rejects custom User-Agent rows and case-insensitive duplicate names", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [
              { name: "User-Agent", value: "bad" },
              { name: "x-forwarded-for", value: "1.2.3.4" },
              { name: "X-Forwarded-For", value: "5.6.7.8" },
            ],
          },
          {},
        ),
      /User-Agent|duplicate/i,
    );
  });
});
