import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  confirmAccessServiceClient,
  createAccessService,
  deleteAccessService,
  fetchAccessServices,
  updateAccessService,
} from './accessServices.js'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
}))

vi.mock('./client', () => ({ api: mocks }))

describe('access service API', () => {
  beforeEach(() => {
    mocks.get.mockReset()
    mocks.post.mockReset()
    mocks.put.mockReset()
    mocks.delete.mockReset()
  })

  it('reads the service collection and sends a distinct idempotency key', async () => {
    mocks.get.mockResolvedValue({ data: { ok: true, services: [{ id: 'svc-1' }] } })
    mocks.post.mockResolvedValue({ data: { id: 'svc-2' } })
    await expect(fetchAccessServices()).resolves.toEqual([{ id: 'svc-1' }])
    await createAccessService({ backend_url: 'http://10.1.0.8:8096' }, 'create-key')
    expect(mocks.post).toHaveBeenCalledWith('/services', { backend_url: 'http://10.1.0.8:8096' }, {
      headers: { 'Idempotency-Key': 'create-key' },
    })
  })

  it('updates and deletes with their own keys', async () => {
    mocks.put.mockResolvedValue({ data: { id: 'svc-1', name: '新名称' } })
    mocks.delete.mockResolvedValue({ data: { id: 'svc-1', summary: '已保存、待生效' } })
    await updateAccessService('svc/1', { name: '新名称' }, 'update-key')
    await deleteAccessService('svc/1', 'delete-key')
    expect(mocks.put).toHaveBeenCalledWith('/services/svc%2F1', { name: '新名称' }, {
      headers: { 'Idempotency-Key': 'update-key' },
    })
    expect(mocks.delete).toHaveBeenCalledWith('/services/svc%2F1', {
      data: {},
      headers: { 'Idempotency-Key': 'delete-key' },
    })
  })

  it('posts client confirmation separately from probe results', async () => {
    mocks.post.mockResolvedValue({ data: { id: 'svc-1', client_confirmation: { confirmed: true, kind: 'user_confirmation' } } })
    const view = await confirmAccessServiceClient('svc-1', { confirmed: true }, 'confirm-key')
    expect(mocks.post).toHaveBeenCalledWith('/services/svc-1/client-confirmation', { confirmed: true }, {
      headers: { 'Idempotency-Key': 'confirm-key' },
    })
    expect(view.client_confirmation.kind).toBe('user_confirmation')
  })
})
