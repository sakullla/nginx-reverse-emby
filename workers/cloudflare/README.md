# Cloudflare Worker Script

This directory contains the dependency-free NRE Cloudflare Worker asset for GitHub Release or repository raw URL distribution. It is not part of the Docker image and must not be copied into container build stages.

## Required Variables

Set these Worker variables before deploying:

| Variable | Required | Description |
| --- | --- | --- |
| `NRE_MASTER_URL` | Yes | Public HTTPS URL of the control-plane Master. A trailing `/panel-api` or `/api` is normalized away by the Worker. |
| `NRE_WORKER_TOKEN` | Yes | Token used by the Worker when proxying to the Master. The Worker sends it as `X-Panel-Token` and never includes it in health or error responses. |

The Worker exposes `/health`, `/healthz`, and `/__nre/health` locally for any HTTP method. All other HTTP requests are proxied to `NRE_MASTER_URL` with method, query string, body, and non-sensitive headers preserved where Cloudflare Workers allows it.

## Deploy With Wrangler

The control panel Worker wizard emits the script URL, expected SHA256, variables, and Wrangler command. For a GitHub Release asset or raw repository URL, download and verify the script checksum first:

```sh
curl -fsSL "$NRE_WORKER_SCRIPT_URL" -o nre-worker.js
sha256sum nre-worker.js
```

Compare the output with the package metadata SHA256 shown in the panel. Do not deploy if the checksum differs.

Then set secrets on the target Worker and deploy the verified local script file with Wrangler:

```sh
wrangler secret put NRE_MASTER_URL --name nre-edge
wrangler secret put NRE_WORKER_TOKEN --name nre-edge
wrangler deploy --name nre-edge --compatibility-date 2026-04-26 nre-worker.js
```

You may also use plain Worker variables instead of secrets for `NRE_MASTER_URL`; keep `NRE_WORKER_TOKEN` as a secret. After deployment, check:

```sh
curl -fsS "https://nre-edge.<your-subdomain>.workers.dev/health"
```

## Release Metadata

Control-plane package records for this file should use:

- `platform`: `cloudflare_worker`
- `arch`: `script`
- `kind`: `worker_script`
- `download_url`: GitHub Release asset URL or raw repository URL for `workers/cloudflare/nre-worker.js`
- `sha256`: 64-character lowercase hex SHA256 of the exact script content

## Container Policy

Cloudflare Worker scripts, Flutter clients, desktop packages, Android APKs, and other client artifacts are distributed through GitHub Release or repository URLs. They are not built into or copied into the NRE Docker image.
