import { describe, expect, it, vi } from 'vitest'
import { runBulk, summarizeBulk } from './runBulk.js'

function deferred() {
  let resolve
  let reject
  const promise = new Promise((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('runBulk', () => {
  it('never runs more than 4 tasks at once', async () => {
    let active = 0
    let peak = 0
    const task = vi.fn(async () => {
      active += 1
      peak = Math.max(peak, active)
      await new Promise((resolve) => setTimeout(resolve, 5))
      active -= 1
    })
    const result = await runBulk([1, 2, 3, 4, 5, 6, 7, 8, 9], task)
    expect(task).toHaveBeenCalledTimes(9)
    expect(peak).toBe(4)
    expect(result.succeeded).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9])
    expect(result.failed).toEqual([])
    expect(result.firstError).toBeNull()
  })

  it('starts only 4 tasks until one finishes', async () => {
    const pending = new Map()
    const task = vi.fn((id) => {
      const d = deferred()
      pending.set(id, d)
      return d.promise
    })
    const run = runBulk([1, 2, 3, 4, 5], task)
    await Promise.resolve()
    expect(task).toHaveBeenCalledTimes(4)
    pending.get(1).resolve()
    await vi.waitFor(() => expect(task).toHaveBeenCalledTimes(5))
    for (const d of pending.values()) d.resolve()
    await run
  })

  it('collects failed ids and the first error without rejecting', async () => {
    const first = new Error('权限不足')
    const task = async (id) => {
      if (id === 'b') throw first
      if (id === 'd') throw new Error('later')
    }
    const result = await runBulk(['a', 'b', 'c', 'd'], task)
    expect(result.succeeded).toEqual(['a', 'c'])
    expect(result.failed).toEqual(['b', 'd'])
    expect(result.firstError).toBe(first)
    expect(result.errors.map((e) => e.id)).toEqual(['b', 'd'])
  })

  it('calls onSettled exactly once after every task has finished', async () => {
    const finished = []
    const onSettled = vi.fn((result) => {
      expect(finished).toHaveLength(6)
      expect(result.failed).toEqual([3])
    })
    await runBulk([1, 2, 3, 4, 5, 6], async (id) => {
      await new Promise((resolve) => setTimeout(resolve, id))
      finished.push(id)
      if (id === 3) throw new Error('x')
    }, { onSettled })
    expect(onSettled).toHaveBeenCalledTimes(1)
  })

  it('reports progress and handles an empty list', async () => {
    const onProgress = vi.fn()
    const onSettled = vi.fn()
    await runBulk([1, 2], async () => {}, { onProgress })
    expect(onProgress).toHaveBeenLastCalledWith({ done: 2, total: 2 })
    const empty = await runBulk([], vi.fn(), { onSettled })
    expect(empty.succeeded).toEqual([])
    expect(onSettled).toHaveBeenCalledTimes(1)
  })
})

describe('summarizeBulk', () => {
  it('summarizes success and partial failure', () => {
    expect(summarizeBulk({ succeeded: [1, 2], failed: [] }, '禁用')).toBe('已禁用 2 项')
    expect(summarizeBulk({ succeeded: [1], failed: [2], firstError: new Error('超时') }, '删除'))
      .toBe('已删除 1 项，失败 1 项：超时')
    expect(summarizeBulk({ succeeded: [], failed: [2], firstError: { response: { data: { message: '拒绝' } } } }, '启用'))
      .toBe('启用失败 1 项：拒绝')
  })
})
