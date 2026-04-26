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

  it('builds GitHub-hosted script deployment output', () => {
    const model = buildWorkerDeployModel({
      workerName: 'nre-edge',
      masterUrl: 'https://panel.example.com',
      token: 'secret',
      packageRecord: {
        platform: 'cloudflare_worker',
        kind: 'worker_script',
        version: '1.1.0',
        download_url: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js',
        sha256: 'a'.repeat(64)
      }
    })
    expect(model.env.NRE_MASTER_URL).toBe('https://panel.example.com')
    expect(model.command).toContain('wrangler deploy')
    expect(model.command).toContain('nre-edge')
    expect(model.scriptUrl).toContain('raw.githubusercontent.com')
    expect(model.sha256).toBe('a'.repeat(64))
  })
})
