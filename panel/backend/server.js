#!/usr/bin/env node
"use strict";

const fs = require("fs");
const http = require("http");
const path = require("path");
const { spawnSync } = require("child_process");

const HOST = process.env.PANEL_BACKEND_HOST || "127.0.0.1";
const PORT = Number(process.env.PANEL_BACKEND_PORT || "18081");
const RULES_FILE =
  process.env.PANEL_RULES_FILE ||
  "/opt/nginx-reverse-emby/panel/data/proxy_rules.csv";
const GENERATOR_SCRIPT =
  process.env.PANEL_GENERATOR_SCRIPT ||
  "/docker-entrypoint.d/25-dynamic-reverse-proxy.sh";
const NGINX_BIN = process.env.PANEL_NGINX_BIN || "nginx";
const AUTO_APPLY = /^(1|true|yes|on)$/i.test(
  process.env.PANEL_AUTO_APPLY || "1",
);
const NGINX_STATUS_URL =
  process.env.NGINX_STATUS_URL || "http://127.0.0.1:18080/nginx_status";
const PANEL_TOKEN = process.env.API_TOKEN || ""; // 鉴权 Token，为空则不启用

function sendJson(res, statusCode, payload) {
  const body = Buffer.from(JSON.stringify(payload), "utf8");
  res.writeHead(statusCode, {
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": String(body.length),
  });
  res.end(body);
}

function errorPayload(message, details) {
  const payload = { ok: false, message };
  if (details) payload.details = details;
  return payload;
}

// 鉴权检查
function isAuthorized(req) {
  if (!PANEL_TOKEN) return true; // 未配置 Token，跳过检查
  const token = req.headers["x-panel-token"];
  return token === PANEL_TOKEN;
}

// 解析 Nginx stub_status 文本
function parseStubStatus(data) {
  const lines = data.split("\n");
  const activeMatch = lines[0].match(/\d+/);
  const requestsLine = lines[2].trim().split(/\s+/);

  return {
    activeConnections: activeMatch ? activeMatch[0] : "0",
    totalRequests: requestsLine.length >= 3 ? requestsLine[2] : "0",
    status: "正常",
  };
}

function getNginxStats() {
  return new Promise((resolve) => {
    http
      .get(NGINX_STATUS_URL, (res) => {
        let data = "";
        res.on("data", (chunk) => (data += chunk));
        res.on("end", () => {
          try {
            resolve(parseStubStatus(data));
          } catch (e) {
            resolve({
              totalRequests: "0",
              status: "获取失败",
              error: e.message,
            });
          }
        });
      })
      .on("error", (err) => {
        resolve({ totalRequests: "0", status: "连接失败", error: err.message });
      });
  });
}

function ensureRulesFile() {
  fs.mkdirSync(path.dirname(RULES_FILE), { recursive: true });
  if (!fs.existsSync(RULES_FILE)) {
    fs.writeFileSync(RULES_FILE, "", "utf8");
  }
}

function loadRulePairs() {
  ensureRulesFile();
  const raw = fs.readFileSync(RULES_FILE, "utf8");
  const lines = raw.split(/\r?\n/);
  const pairs = [];

  for (const lineRaw of lines) {
    const line = lineRaw.trim();
    if (!line || line.startsWith("#")) continue;
    const commaIndex = line.indexOf(",");
    if (commaIndex === -1) continue;
    const frontend = line.slice(0, commaIndex).trim();
    const backend = line.slice(commaIndex + 1).trim();
    if (frontend && backend) pairs.push([frontend, backend]);
  }

  return pairs;
}

function saveRulePairs(pairs) {
  ensureRulesFile();
  const content = pairs
    .map(([frontend, backend]) => `${frontend},${backend}`)
    .join("\n");
  fs.writeFileSync(RULES_FILE, content ? `${content}\n` : "", "utf8");
}

function listRules() {
  return loadRulePairs().map(([frontend_url, backend_url], idx) => ({
    id: idx + 1,
    frontend_url,
    backend_url,
  }));
}

function validateUrl(value) {
  try {
    const u = new URL(value);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

function runChecked(command, args) {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (result.error) {
    throw new Error(result.error.message);
  }
  if (result.status !== 0) {
    const details = (
      result.stderr ||
      result.stdout ||
      `exit code ${result.status}`
    ).trim();
    throw new Error(details);
  }
}

function applyNginxConfig() {
  runChecked(GENERATOR_SCRIPT, []);
  runChecked(NGINX_BIN, ["-t"]);
  runChecked(NGINX_BIN, ["-s", "reload"]);
}

function parseJsonBody(req) {
  return new Promise((resolve, reject) => {
    let raw = "";
    req.setEncoding("utf8");
    req.on("data", (chunk) => {
      raw += chunk;
      if (raw.length > 1024 * 1024) {
        reject(new Error("request body too large"));
      }
    });
    req.on("end", () => {
      if (!raw) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch {
        reject(new Error("invalid JSON body"));
      }
    });
    req.on("error", (err) => reject(err));
  });
}

function extractRuleId(urlPath) {
  const m = urlPath.match(/^\/api\/rules\/(\d+)$/);
  if (!m) return null;
  return Number(m[1]);
}

async function handleRequest(req, res) {
  const urlPath = (req.url || "").split("?")[0];

  // 1. 公开接口：验证 Token 是否有效 (用于前端检查)
  if (req.method === "GET" && urlPath === "/api/auth/verify") {
    const authorized = isAuthorized(req);
    sendJson(res, authorized ? 200 : 401, { ok: authorized });
    return;
  }

  // 2. 鉴权拦截
  if (!isAuthorized(req)) {
    sendJson(
      res,
      401,
      errorPayload("Unauthorized: Invalid or missing X-Panel-Token"),
    );
    return;
  }

  if (req.method === "GET" && urlPath === "/api/health") {
    sendJson(res, 200, { ok: true });
    return;
  }

  if (req.method === "GET" && urlPath === "/api/stats") {
    const stats = await getNginxStats();
    sendJson(res, 200, { ok: true, stats });
    return;
  }

  if (req.method === "GET" && urlPath === "/api/rules") {
    sendJson(res, 200, { ok: true, rules: listRules() });
    return;
  }

  if (req.method === "POST" && urlPath === "/api/apply") {
    try {
      applyNginxConfig();
      sendJson(res, 200, { ok: true, message: "applied" });
    } catch (err) {
      sendJson(
        res,
        400,
        errorPayload(
          "failed to apply nginx config",
          String(err.message || err),
        ),
      );
    }
    return;
  }

  if (req.method === "POST" && urlPath === "/api/rules") {
    try {
      const body = await parseJsonBody(req);
      const frontend = String(body.frontend_url || "").trim();
      const backend = String(body.backend_url || "").trim();

      if (!validateUrl(frontend) || !validateUrl(backend)) {
        sendJson(
          res,
          400,
          errorPayload(
            "frontend_url and backend_url must be valid http/https URLs",
          ),
        );
        return;
      }

      const pairs = loadRulePairs();
      pairs.push([frontend, backend]);
      saveRulePairs(pairs);
      const created = {
        id: pairs.length,
        frontend_url: frontend,
        backend_url: backend,
      };

      if (AUTO_APPLY) {
        try {
          applyNginxConfig();
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "rule saved but failed to apply nginx config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 201, { ok: true, rule: created });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PUT") {
    const ruleId = extractRuleId(urlPath);
    if (!ruleId) {
      sendJson(res, 404, errorPayload("not found"));
      return;
    }

    try {
      const body = await parseJsonBody(req);
      const frontend = String(body.frontend_url || "").trim();
      const backend = String(body.backend_url || "").trim();

      if (!validateUrl(frontend) || !validateUrl(backend)) {
        sendJson(
          res,
          400,
          errorPayload(
            "frontend_url and backend_url must be valid http/https URLs",
          ),
        );
        return;
      }

      const pairs = loadRulePairs();
      if (ruleId < 1 || ruleId > pairs.length) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }

      pairs[ruleId - 1] = [frontend, backend];
      saveRulePairs(pairs);

      if (AUTO_APPLY) {
        try {
          applyNginxConfig();
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "rule updated but failed to apply nginx config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 200, {
        ok: true,
        rule: { id: ruleId, frontend_url: frontend, backend_url: backend },
      });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE") {
    const ruleId = extractRuleId(urlPath);
    if (!ruleId) {
      sendJson(res, 404, errorPayload("not found"));
      return;
    }

    const pairs = loadRulePairs();
    if (ruleId < 1 || ruleId > pairs.length) {
      sendJson(res, 404, errorPayload("rule id not found"));
      return;
    }

    const deleted = pairs.splice(ruleId - 1, 1)[0];
    saveRulePairs(pairs);

    if (AUTO_APPLY) {
      try {
        applyNginxConfig();
      } catch (err) {
        sendJson(
          res,
          400,
          errorPayload(
            "rule deleted but failed to apply nginx config",
            String(err.message || err),
          ),
        );
        return;
      }
    }

    sendJson(res, 200, {
      ok: true,
      rule: { id: ruleId, frontend_url: deleted[0], backend_url: deleted[1] },
    });
    return;
  }

  sendJson(res, 404, errorPayload("not found"));
}

ensureRulesFile();

http
  .createServer((req, res) => {
    handleRequest(req, res).catch((err) => {
      sendJson(
        res,
        500,
        errorPayload("internal server error", String(err.message || err)),
      );
    });
  })
  .listen(PORT, HOST);
