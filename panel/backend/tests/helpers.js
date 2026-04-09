"use strict";

const fc = require("fast-check");
const { spawn } = require("node:child_process");
const fsp = require("node:fs/promises");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const { once } = require("node:events");

const TEST_SERVER_CERT_PEM = [
  "-----BEGIN CERTIFICATE-----",
  "MIIDQjCCAiqgAwIBAgIBATANBgkqhkiG9w0BAQsFADA3MRAwDgYDVQQKEwdFeGFt",
  "cGxlMSMwIQYDVQQDExpyZWxheS11cGxvYWRlZC5leGFtcGxlLmNvbTAgFw0yMDAx",
  "MDEwMDAwMDBaGA8yMDk5MTIzMTIzNTk1OVowNzEQMA4GA1UEChMHRXhhbXBsZTEj",
  "MCEGA1UEAxMacmVsYXktdXBsb2FkZWQuZXhhbXBsZS5jb20wggEiMA0GCSqGSIb3",
  "DQEBAQUAA4IBDwAwggEKAoIBAQDC+mbfA7s+6XAW269tSRdIZIqhab+mQhhoHKPt",
  "alLGKvurpxWvUla40D49T4mo9yji63d99rqWDYKJ0kTXj6H0gFToHNF/dE+eWj8T",
  "bE1xt2d6InHETd7RhBGPUDEI3mle3rOYNfps7sZA4Qid3OUbW/eyxKWrumI0L2fd",
  "Z4gytSZIwDPTzgcwkuDsmL0wIZmgZXBLZclVpyNnXV6MUjn6G0Uusax+QwT0woEI",
  "7tD93g3L/IXPNPJ9u7VX+NSScZt9xXlqCYzte+OqWZIngR8HcfBlBFSoXkcdWMQx",
  "cm7KNVejRj4QkH6ht2UcypwfSNtD6cuHvZQlOl7BDaQ6IahNAgMBAAGjVzBVMA4G",
  "A1UdDwEB/wQEAwIFoDATBgNVHSUEDDAKBggrBgEFBQcDATAPBgNVHRMBAf8EBTAD",
  "AQH/MB0GA1UdDgQWBBTm6LKcjbr/O4yLiptCCcmtIlUjazANBgkqhkiG9w0BAQsF",
  "AAOCAQEAO3MN6+cIR/KDGJvzFJ444l06IHX1g6dEvbusVXBI3qkYH7qfPAReJ05q",
  "tZ9XjiGiFoCyxUuBGP24sClSxTzQg7iC8KpmwpKE1xqebTPrneVJL5pgE97MDnvL",
  "ofLBBg+tuisSQnenZyrNQi749LsYu91ogszu1Y6Zn4u5bpaWVl/bYnAMbkTzhyZT",
  "5WA/Dk7//nPzUkR9ig0bwg7WJZDqkDwQe6VZTiy9uh8Ow2ebKMPaZVYOkYQqV3pz",
  "wDkkeWcpdllqrs9pLh/G0x//3a7WuBh23uBryg4sMf6vxJb/A5Ws+KNubTvrchCk",
  "EF93qYMp9r6DiJE1A7KrUlaJxe+ovw==",
  "-----END CERTIFICATE-----",
].join("\n");

const TEST_SERVER_KEY_PEM = [
  "-----BEGIN RSA PRIVATE KEY-----",
  "MIIEpQIBAAKCAQEAwvpm3wO7PulwFtuvbUkXSGSKoWm/pkIYaByj7WpSxir7q6cV",
  "r1JWuNA+PU+JqPco4ut3ffa6lg2CidJE14+h9IBU6BzRf3RPnlo/E2xNcbdneiJx",
  "xE3e0YQRj1AxCN5pXt6zmDX6bO7GQOEIndzlG1v3ssSlq7piNC9n3WeIMrUmSMAz",
  "084HMJLg7Ji9MCGZoGVwS2XJVacjZ11ejFI5+htFLrGsfkME9MKBCO7Q/d4Ny/yF",
  "zzTyfbu1V/jUknGbfcV5agmM7XvjqlmSJ4EfB3HwZQRUqF5HHVjEMXJuyjVXo0Y+",
  "EJB+obdlHMqcH0jbQ+nLh72UJTpewQ2kOiGoTQIDAQABAoIBAAmhwBI1R3s4ofJn",
  "INfnu/A2E0kdBbwrWLRP8eMpFPS4K92Tb/VMvn77vo9dzgGcUBdBpZIB7b665R90",
  "1TTG4ivHaSpcPhcrQkGi2KnXeE3tTv3QFMmrRR4ZhZqMThfPkOoAW2PiCsB13TJY",
  "S4os3t6GoQpiP4LnvrEwRFPCKQ7EIQMBNd/uUnbOPzCKL1KgdKZGJ6iGdtnfjVjR",
  "eJXk7+hKoQm6ohUkUbhpkeQhgp47tSfJjdmNB0KoTcN3LxOo2FAVSf76eWuAd1qg",
  "OAPQcmY96rn3CI9G2GIghm08zVh6RgImQHgXNpKBoQxxc9xxOmrOqZ6xXcXy+7Vq",
  "oaca79sCgYEA74SBL2wK7nR0XLTNBYLYM2fXthNASjdYaezCr2nVOqeNOnA/Zitp",
  "amX+bxRTsoLStuW4G0ecCARAWa5uUmczg/rci6mbhx7chi2P+ASFT2IPkCTiDHTF",
  "0ZYHYE1S+buFiY2a5lnBxYCdQWnAjlEZTDHOM/Aiia+WkH5plU3hOX8CgYEA0GVC",
  "8Nzu6VwbgxaYSofxK86cXIDOIVmo4+l6vv/kUodzuVlgiaICnTpVBvLLiCSXYx3g",
  "+HVDDqLK6X+i5jWHPKRZj/0zMzyVQVR+boZr4XZPhP1L32eiIgeAyllzHDxhuJ9Q",
  "wTXdLkJWJttJmtdPytjucnBtBdksRhqaU6iOzDMCgYEAxTQcxTW4vmIlmFrIXxw+",
  "8/wwr9mj2jc9VWE5XgHOLQ/dCNt4Z5+gmJjHZx+eVeC+qxXygotwHW2aqfwjGzeb",
  "Q7QdN+R6iELRoKwM2FCojhaX579mWokegpR7GEAx7CoIJZvwiG4oS3u8fioa/1Io",
  "eQKc20iAt0pZtjhOqD5KDPMCgYEAqAPcMqGNpWtzav7+jaiIks8jVZkrl8vX1Nja",
  "878P8FHwxVD/+jc6cFUlVFLQMdV+kJT4WpkAFX6+pf8X8Q7bF9NRujtj2j1QALoE",
  "rUuHEuH2PryRPW8qUtFFzt7LZcpw5w7bZsrspm0pVG6cK1DIrjy0EmP+Iib0ARlV",
  "r3lIl+0CgYEAx7V9488af8Nul8Pq/aOP2HaDoQvH7kP7RLc+OY7qwTZr9EP7X4Q+",
  "J+Wz88vAHPHcxX+BFBYhph8w5mplCA/55MxO9rJeH2KLZoozRrGBrJahXEiuhcc2",
  "CWptsnMsbYI0Qt62/SphaTXABUG4ft4bj53rPmkaRsNqVexs90R64mk=",
  "-----END RSA PRIVATE KEY-----",
].join("\n");

const TEST_CA_CHAIN_PEM = [
  "-----BEGIN CERTIFICATE-----",
  "MIIDQjCCAiqgAwIBAgIBATANBgkqhkiG9w0BAQsFADA3MRAwDgYDVQQKEwdFeGFt",
  "cGxlMSMwIQYDVQQDExpyZWxheS11cGxvYWRlZC5leGFtcGxlLmNvbTAgFw0yMDAx",
  "MDEwMDAwMDBaGA8yMDk5MTIzMTIzNTk1OVowNzEQMA4GA1UEChMHRXhhbXBsZTEj",
  "MCEGA1UEAxMacmVsYXktdXBsb2FkZWQuZXhhbXBsZS5jb20wggEiMA0GCSqGSIb3",
  "DQEBAQUAA4IBDwAwggEKAoIBAQDMP0mgII+PtE9NAVV/obvHBDXqPjjZpLrO0lfu",
  "AFgXijRYf7x02n5dk7bt+BFL6KH/PE/uW/TqpeaFjnmellA006yKN26sEIJOe8df",
  "3blSfssUyCnHOnEisx+hrYfbUNtffIn5989PrTAw198U1DVvmGqh7H5iz0c7WsMU",
  "OF8gRWlAHiDpKGehDBbbK4M2tEPzqf51O3VHamcihXMiV6BRjVg7z1OpYBtMZxQS",
  "2pKTRicIjupnL2kLxBfV24mzS9bCmPrexL7xAcijzlxIGBXDv4SVczneAO2euRKL",
  "Gq4iifnBY+wi6hAIFVCxS9LjDfc8UpkL35mZhDZNiwxTVQMHAgMBAAGjVzBVMA4G",
  "A1UdDwEB/wQEAwIFoDATBgNVHSUEDDAKBggrBgEFBQcDATAPBgNVHRMBAf8EBTAD",
  "AQH/MB0GA1UdDgQWBBQAxrT7YCU+sYI9RNd245LaKHDvXDANBgkqhkiG9w0BAQsF",
  "AAOCAQEAQ52S91JBskcaloWagH7Tsc7rikb/UMBIVsDcMzG+G/eDR/IuyxTFjf8e",
  "MKpBgfkTOdaHJbCst7b+DUGZJL05LMeK8/MfCR2eAPmc3eexE1igpIW1WDnE0d6U",
  "OmqKf6CPCZxkWeev04Q0Ki8jNbAPr/qVeyI9R2NwMvIqRdK76Ege8D/Rp8YxJdR/",
  "+U7PkThxyN/O5uhEURKkltXtBbzfOycXL1V4Hs/6nwl0jIgHdw7+KUBKRNmdRK0o",
  "fvzL/96KBpQR+1BI9WXgLZqvq+uzbuoxE0AT0LPJ+U1dNUEbWQ/8E5gIp0cmhlAz",
  "1njlaHGd7Fp5K9TyzasnkhYmcl1aEg==",
  "-----END CERTIFICATE-----",
].join("\n");

const TEST_SERVER_CHAIN_PEM = [TEST_SERVER_CERT_PEM, TEST_CA_CHAIN_PEM].join("\n");

const SQLITE_TARGET = ":memory:";
const safeString = fc.string({ maxLength: 50 }).map((s) => s.replace(/\0/g, ""));
const nonEmptyString = fc.string({ minLength: 1, maxLength: 50 }).map((s) => s.replace(/\0/g, ""));

function detectSqliteAvailability() {
  try {
    const storage = require("../storage-sqlite");
    storage.init(SQLITE_TARGET);
    storage.close();
    return true;
  } catch (_) {
    return false;
  }
}

function loadFreshStorage(modulePath, initArg) {
  const resolved = require.resolve(modulePath);
  delete require.cache[resolved];
  const storage = require(modulePath);
  if (initArg !== undefined) {
    storage.init(initArg);
  }
  return storage;
}

function closeQuietly(storage) {
  try {
    storage.close();
  } catch (_) {
    // ignore test teardown noise
  }
}

function dedupById(items, key = "id") {
  const map = new Map();
  for (const item of items) {
    map.set(item[key], item);
  }
  return [...map.values()];
}

function getNumRuns(suiteName, fallback) {
  const suiteKey = `PANEL_BACKEND_TEST_NUM_RUNS_${String(suiteName).toUpperCase()}`;
  const raw = process.env[suiteKey] || process.env.PANEL_BACKEND_TEST_NUM_RUNS;
  const parsed = Number.parseInt(raw, 10);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback;
}

async function getFreePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  const port = address && typeof address === "object" ? address.port : 0;
  await new Promise((resolve, reject) => {
    server.close((err) => (err ? reject(err) : resolve()));
  });
  if (!port) {
    throw new Error("failed to get free port");
  }
  return port;
}

async function waitForServer(url, serverProcess, readStderr) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (serverProcess.exitCode !== null) {
      throw new Error(
        `server exited early with code ${serverProcess.exitCode}: ${readStderr()}`,
      );
    }
    try {
      const response = await fetch(url);
      if (response.ok) {
        return;
      }
    } catch (_) {
      // ignore connection failures until timeout
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`server did not become ready: ${url}`);
}

async function writeJson(filePath, value) {
  await fsp.mkdir(path.dirname(filePath), { recursive: true });
  await fsp.writeFile(filePath, JSON.stringify(value, null, 2), "utf8");
}

function createDeterministicChildEnv(envOverrides, port, dataRoot) {
  const passthroughKeys = [
    "PATH",
    "Path",
    "PATHEXT",
    "SystemRoot",
    "SYSTEMROOT",
    "WINDIR",
    "windir",
    "COMSPEC",
    "TEMP",
    "TMP",
    "HOME",
    "USERPROFILE",
    "APPDATA",
    "LOCALAPPDATA",
    "ProgramData",
    "PROGRAMDATA",
  ];
  const baseEnv = {};
  for (const key of passthroughKeys) {
    if (process.env[key] !== undefined) {
      baseEnv[key] = process.env[key];
    }
  }
  return {
    ...baseEnv,
    API_TOKEN: "",
    AGENT_API_TOKEN: "",
    MASTER_REGISTER_TOKEN: "",
    PANEL_REGISTER_TOKEN: "",
    PANEL_BACKEND_HOST: "127.0.0.1",
    PANEL_BACKEND_PORT: String(port),
    PANEL_DATA_ROOT: dataRoot,
    PANEL_STORAGE_BACKEND: "json",
    PROXY_PASS_PROXY_HEADERS: "",
    MASTER_LOCAL_AGENT_ENABLED: "1",
    ...envOverrides,
  };
}

async function withBackendServer(options, testFn) {
  const port = await getFreePort();
  const dataRoot = await fsp.mkdtemp(path.join(os.tmpdir(), "panel-backend-test-"));
  const envOverrides = options?.env || {};

  if (options?.proxyRules) {
    await writeJson(path.join(dataRoot, "proxy_rules.json"), options.proxyRules);
  }
  if (options?.agents) {
    await writeJson(path.join(dataRoot, "agents.json"), options.agents);
  }
  if (options?.agentRulesByAgentId && typeof options.agentRulesByAgentId === "object") {
    for (const [agentId, rules] of Object.entries(options.agentRulesByAgentId)) {
      await writeJson(path.join(dataRoot, "agent_rules", `${agentId}.json`), rules);
    }
  }
  if (options?.relayListenersByAgentId && typeof options.relayListenersByAgentId === "object") {
    for (const [agentId, listeners] of Object.entries(options.relayListenersByAgentId)) {
      await writeJson(path.join(dataRoot, "relay_listeners", `${agentId}.json`), listeners);
    }
  }
  if (options?.l4RulesByAgentId && typeof options.l4RulesByAgentId === "object") {
    for (const [agentId, rules] of Object.entries(options.l4RulesByAgentId)) {
      await writeJson(path.join(dataRoot, "l4_agent_rules", `${agentId}.json`), rules);
    }
  }
  if (options?.managedCertificates) {
    await writeJson(
      path.join(dataRoot, "managed_certificates.json"),
      options.managedCertificates,
    );
  }
  if (options?.managedCertificateMaterial && typeof options.managedCertificateMaterial === "object") {
    for (const [domain, material] of Object.entries(options.managedCertificateMaterial)) {
      if (!material) continue;
      const certDir = path.join(dataRoot, "managed_certificates", domain);
      await fsp.mkdir(certDir, { recursive: true });
      if (material.cert_pem !== undefined) {
        await fsp.writeFile(path.join(certDir, "cert"), String(material.cert_pem), "utf8");
      }
      if (material.key_pem !== undefined) {
        await fsp.writeFile(path.join(certDir, "key"), String(material.key_pem), "utf8");
      }
    }
  }
  if (options?.versionPolicies) {
    await writeJson(path.join(dataRoot, "version_policies.json"), options.versionPolicies);
  }

  const serverProcess = spawn(process.execPath, ["server.js"], {
    cwd: path.resolve(__dirname, ".."),
    env: createDeterministicChildEnv(envOverrides, port, dataRoot),
    stdio: ["ignore", "pipe", "pipe"],
  });

  const baseUrl = `http://127.0.0.1:${port}`;
  let stderr = "";
  serverProcess.stderr.on("data", (chunk) => {
    stderr += String(chunk);
  });

  try {
    await waitForServer(
      `${baseUrl}${options?.readyPath || "/api/info"}`,
      serverProcess,
      () => stderr,
    );
    await testFn({ baseUrl, dataRoot });
  } finally {
    if (serverProcess.exitCode === null) {
      serverProcess.kill("SIGTERM");
      await once(serverProcess, "exit");
    }
    await fsp.rm(dataRoot, { recursive: true, force: true });
  }
}

module.exports = {
  SQLITE_TARGET,
  canRunSqlite: detectSqliteAvailability(),
  safeString,
  nonEmptyString,
  loadFreshStorage,
  closeQuietly,
  dedupById,
  getNumRuns,
  TEST_SERVER_CERT_PEM,
  TEST_SERVER_KEY_PEM,
  TEST_CA_CHAIN_PEM,
  TEST_SERVER_CHAIN_PEM,
  withBackendServer,
};
