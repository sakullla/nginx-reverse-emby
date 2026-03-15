#!/usr/bin/env node
"use strict";

const fs = require("fs");
const http = require("http");
const path = require("path");
const { spawnSync } = require("child_process");

const HOST = process.env.PANEL_BACKEND_HOST || "127.0.0.1";
const PORT = Number(process.env.PANEL_BACKEND_PORT || "18081");
const DATA_ROOT = "/opt/nginx-reverse-emby/panel/data";
const RULES_JSON =
  process.env.PANEL_RULES_JSON || path.join(DATA_ROOT, "proxy_rules.json");
const RULES_CSV =
  process.env.PANEL_RULES_FILE || path.join(DATA_ROOT, "proxy_rules.csv");
const GENERATOR_SCRIPT =
  process.env.PANEL_GENERATOR_SCRIPT ||
  "/docker-entrypoint.d/25-dynamic-reverse-proxy.sh";
const NGINX_BIN = process.env.PANEL_NGINX_BIN || "nginx";
const AUTO_APPLY = /^(1|true|yes|on)$/i.test(
  process.env.PANEL_AUTO_APPLY || "1",
);
const NGINX_STATUS_URL =
  process.env.NGINX_STATUS_URL || "http://127.0.0.1:18080/nginx_status";
const PANEL_TOKEN = process.env.API_TOKEN || "";

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

function isAuthorized(req) {
  if (!PANEL_TOKEN) return true;
  const token = req.headers["x-panel-token"];
  return token === PANEL_TOKEN;
}

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

function ensureDataDir() {
  fs.mkdirSync(DATA_ROOT, { recursive: true });
}

function migrateCsvToJson() {
  if (!fs.existsSync(RULES_JSON) && fs.existsSync(RULES_CSV)) {
    console.log("Migrating proxy_rules.csv to proxy_rules.json...");
    try {
      const raw = fs.readFileSync(RULES_CSV, "utf8");
      const lines = raw.split(/\r?\n/);
      const rules = [];
      let id = 1;

      for (const lineRaw of lines) {
        const line = lineRaw.trim();
        if (!line || line.startsWith("#")) continue;
        const commaIndex = line.indexOf(",");
        if (commaIndex === -1) continue;
        const frontend = line.slice(0, commaIndex).trim();
        const backend = line.slice(commaIndex + 1).trim();
        if (frontend && backend) {
          rules.push({
            id: id++,
            frontend_url: frontend,
            backend_url: backend,
            enabled: true,
            tags: [],
            proxy_redirect: true,
          });
        }
      }
      fs.writeFileSync(RULES_JSON, JSON.stringify(rules, null, 2), "utf8");
      console.log(`Migration complete. ${rules.length} rules migrated.`);
    } catch (err) {
      console.error("Migration failed:", err);
    }
  }
}

function loadRules() {
  ensureDataDir();
  migrateCsvToJson();
  if (!fs.existsSync(RULES_JSON)) {
    return [];
  }
  try {
    const data = fs.readFileSync(RULES_JSON, "utf8");
    return JSON.parse(data);
  } catch (err) {
    console.error("Error loading rules.json:", err);
    return [];
  }
}

function saveRules(rules) {
  ensureDataDir();
  fs.writeFileSync(RULES_JSON, JSON.stringify(rules, null, 2), "utf8");
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
      if (raw.length > 1024 * 1024) reject(new Error("request body too large"));
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

  if (req.method === "GET" && urlPath === "/api/auth/verify") {
    const authorized = isAuthorized(req);
    sendJson(res, authorized ? 200 : 401, { ok: authorized });
    return;
  }

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
    sendJson(res, 200, { ok: true, rules: loadRules() });
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
      const tags = Array.isArray(body.tags)
        ? body.tags.map((t) => String(t).trim()).filter(Boolean)
        : [];
      const enabled = body.enabled !== false;
      const proxy_redirect = body.proxy_redirect !== false;

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

      const rules = loadRules();
      const maxId = rules.reduce((max, r) => Math.max(max, r.id), 0);
      const newRule = {
        id: maxId + 1,
        frontend_url: frontend,
        backend_url: backend,
        enabled,
        tags,
        proxy_redirect,
      };
      rules.push(newRule);
      saveRules(rules);

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
      sendJson(res, 201, { ok: true, rule: newRule });
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
      const rules = loadRules();
      const index = rules.findIndex((r) => r.id === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }

      const frontend =
        body.frontend_url !== undefined
          ? String(body.frontend_url).trim()
          : rules[index].frontend_url;
      const backend =
        body.backend_url !== undefined
          ? String(body.backend_url).trim()
          : rules[index].backend_url;
      const tags = Array.isArray(body.tags)
        ? body.tags.map((t) => String(t).trim()).filter(Boolean)
        : rules[index].tags;
      const enabled =
        body.enabled !== undefined ? !!body.enabled : rules[index].enabled;
      const proxy_redirect =
        body.proxy_redirect !== undefined
          ? !!body.proxy_redirect
          : rules[index].proxy_redirect !== false;

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

      rules[index] = {
        ...rules[index],
        frontend_url: frontend,
        backend_url: backend,
        enabled,
        tags,
        proxy_redirect,
      };
      saveRules(rules);

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
      sendJson(res, 200, { ok: true, rule: rules[index] });
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

    const rules = loadRules();
    const index = rules.findIndex((r) => r.id === ruleId);
    if (index === -1) {
      sendJson(res, 404, errorPayload("rule id not found"));
      return;
    }

    const deleted = rules.splice(index, 1)[0];
    saveRules(rules);

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
    sendJson(res, 200, { ok: true, rule: deleted });
    return;
  }

  sendJson(res, 404, errorPayload("not found"));
}

ensureDataDir();
migrateCsvToJson();

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
