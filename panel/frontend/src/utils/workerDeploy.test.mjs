import { describe, expect, it } from 'vitest'
import { buildWorkerDeployModel, validateWorkerDeployInput } from './workerDeploy.js'

describe('workerDeploy', () => {
  it('validates required Worker deployment fields', () => {
    expect(validateWorkerDeployInput({
      workerName: '',
      masterUrl: 'not-a-url',
      token: '',
      packageRecord: null
    })).toEqual({
      workerName: '请输入 Worker 名称',
      masterUrl: '请输入有效的 https Master URL',
      token: '请输入 Worker 访问令牌',
      packageRecord: '请选择 Cloudflare Worker 脚本包'
    })
  })

  it('rejects Worker script packages without deployable artifact metadata', () => {
    expect(validateWorkerDeployInput({
      workerName: 'nre-edge',
      masterUrl: 'https://panel.example.com',
      token: 'secret',
      packageRecord: {
        platform: 'cloudflare_worker',
        arch: 'script',
        kind: 'worker_script'
      }
    })).toEqual({
      packageRecord: '请选择有效的 Cloudflare Worker 脚本包'
    })
  })

  it('rejects unsafe Worker names', () => {
    expect(validateWorkerDeployInput({
      workerName: 'nre-edge; rm -rf /',
      masterUrl: 'https://panel.example.com',
      token: 'secret',
      packageRecord: {
        platform: 'cloudflare_worker',
        arch: 'script',
        kind: 'worker_script',
        download_url: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js',
        sha256: 'a'.repeat(64)
      }
    })).toEqual({
      workerName: 'Worker 名称仅支持小写字母、数字和连字符，且需以字母或数字开头和结尾'
    })
  })

  it('rejects Worker script packages with non-script arch', () => {
    expect(validateWorkerDeployInput({
      workerName: 'nre-edge',
      masterUrl: 'https://panel.example.com',
      token: 'secret',
      packageRecord: {
        platform: 'cloudflare_worker',
        arch: 'amd64',
        kind: 'worker_script',
        download_url: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js',
        sha256: 'a'.repeat(64)
      }
    })).toEqual({
      packageRecord: '请选择 Cloudflare Worker 脚本包'
    })
  })

  it('builds GitHub-hosted script deployment output', () => {
    const model = buildWorkerDeployModel({
      workerName: 'nre-edge',
      masterUrl: 'https://panel.example.com/',
      token: 'secret',
      packageRecord: {
        platform: 'cloudflare_worker',
        arch: 'script',
        kind: 'worker_script',
        version: '1.1.0',
        download_url: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js',
        sha256: 'a'.repeat(64)
      }
    })
    expect(model).toEqual({
      workerName: 'nre-edge',
      scriptUrl: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js',
      sha256: 'a'.repeat(64),
      env: {
        NRE_MASTER_URL: 'https://panel.example.com',
        NRE_WORKER_TOKEN: 'secret'
      },
      commandArgs: [
        'wrangler deploy',
        '--name',
        'nre-edge',
        '--compatibility-date 2026-04-26',
        'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js'
      ],
      command: 'wrangler deploy --name nre-edge --compatibility-date 2026-04-26 https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js'
    })
  })
})
