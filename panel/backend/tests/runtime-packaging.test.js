"use strict";

const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const fsp = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const { withBackendServer } = require("./helpers");

async function withRuntimeFixture(testFn) {
  const rootDir = await fsp.mkdtemp(path.join(os.tmpdir(), "panel-runtime-test-"));
  const distDir = path.join(rootDir, "frontend-dist");
  const assetDir = path.join(rootDir, "agent-assets");
  await fsp.mkdir(path.join(distDir, "assets"), { recursive: true });
  await fsp.mkdir(assetDir, { recursive: true });
  await fsp.writeFile(
    path.join(distDir, "index.html"),
    "<!doctype html><html><body><div id=app>control-plane</div></body></html>",
    "utf8",
  );
  await fsp.writeFile(path.join(distDir, "assets", "app.js"), "console.log('panel');", "utf8");
  await fsp.writeFile(
    path.join(assetDir, "nre-agent-linux-amd64"),
    Buffer.from([0x7f, 0x45, 0x4c, 0x46, 0x01, 0x02, 0x03]),
  );

  try {
    await testFn({ distDir, assetDir });
  } finally {
    await fsp.rm(rootDir, { recursive: true, force: true });
  }
}

describe("control-plane runtime packaging endpoints", () => {
  it("serves panel-api aliases, a public health route, and a Go join script", async () => {
    await withRuntimeFixture(async ({ distDir, assetDir }) => {
      await withBackendServer(
        {
          env: {
            PANEL_ROLE: "master",
            PANEL_FRONTEND_DIST_DIR: distDir,
            PANEL_PUBLIC_AGENT_ASSETS_DIR: assetDir,
          },
        },
        async ({ baseUrl }) => {
          const healthResponse = await fetch(`${baseUrl}/panel-api/health`, {
            method: "HEAD",
          });
          assert.equal(healthResponse.status, 200);

          const infoResponse = await fetch(`${baseUrl}/panel-api/info`);
          assert.equal(infoResponse.status, 200);
          const infoPayload = await infoResponse.json();
          assert.equal(infoPayload.ok, true);
          assert.equal(infoPayload.role, "master");
          assert.equal(infoPayload.local_apply_runtime, "go-agent");

          const scriptResponse = await fetch(`${baseUrl}/panel-api/public/join-agent.sh`);
          assert.equal(scriptResponse.status, 200);
          const script = await scriptResponse.text();
          assert.match(script, /nre-agent/);
          assert.match(script, /DEFAULT_ASSET_BASE_URL="http:\/\/127\.0\.0\.1:\d+\/panel-api\/public\/agent-assets"/);
          assert.match(script, /ASSET_NAME="nre-agent-\$PLATFORM-\$ARCH"/);
          assert.doesNotMatch(script, /light-agent\.js/);
        },
      );
    });
  });

  it("serves built frontend assets, SPA fallback, and binary Go agent downloads", async () => {
    await withRuntimeFixture(async ({ distDir, assetDir }) => {
      await withBackendServer(
        {
          env: {
            PANEL_ROLE: "master",
            PANEL_FRONTEND_DIST_DIR: distDir,
            PANEL_PUBLIC_AGENT_ASSETS_DIR: assetDir,
          },
        },
        async ({ baseUrl }) => {
          const assetResponse = await fetch(`${baseUrl}/assets/app.js`);
          assert.equal(assetResponse.status, 200);
          assert.equal(await assetResponse.text(), "console.log('panel');");

          const spaResponse = await fetch(`${baseUrl}/agents/remote-1`);
          assert.equal(spaResponse.status, 200);
          const html = await spaResponse.text();
          assert.match(html, /control-plane/);

          const binaryResponse = await fetch(
            `${baseUrl}/panel-api/public/agent-assets/nre-agent-linux-amd64`,
          );
          assert.equal(binaryResponse.status, 200);
          assert.equal(
            binaryResponse.headers.get("content-type"),
            "application/octet-stream",
          );
          const bytes = Buffer.from(await binaryResponse.arrayBuffer());
          assert.deepEqual([...bytes], [0x7f, 0x45, 0x4c, 0x46, 0x01, 0x02, 0x03]);
        },
      );
    });
  });
});
