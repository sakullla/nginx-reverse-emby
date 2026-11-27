export const BULK_CONCURRENCY = 4

/**
 * Run `task(id)` for every id with bounded concurrency. Never rejects: each
 * failure is collected so callers can keep failed ids selected and show one
 * summary instead of one toast per item.
 *
 * @param {Array<string|number>} ids
 * @param {(id: string|number) => Promise<unknown>} task
 * @param {{
 *   concurrency?: number,
 *   onProgress?: (progress: { done: number, total: number }) => void,
 *   onSettled?: (result: BulkResult) => void | Promise<void>
 * }} [options]
 * @returns {Promise<BulkResult>}
 *
 * @typedef {{
 *   succeeded: Array<string|number>,
 *   failed: Array<string|number>,
 *   errors: Array<{ id: string|number, error: unknown }>,
 *   firstError: unknown
 * }} BulkResult
 */
export async function runBulk(ids, task, options = {}) {
  const list = Array.isArray(ids) ? [...ids] : []
  const limit = Math.max(1, Math.floor(options.concurrency ?? BULK_CONCURRENCY))
  const outcomes = new Array(list.length)
  let cursor = 0
  let done = 0

  async function worker() {
    while (cursor < list.length) {
      const index = cursor++
      const id = list[index]
      try {
        await task(id)
        outcomes[index] = { id, ok: true }
      } catch (error) {
        outcomes[index] = { id, ok: false, error }
      }
      done += 1
      options.onProgress?.({ done, total: list.length })
    }
  }

  const workers = Array.from({ length: Math.min(limit, list.length) }, () => worker())
  await Promise.all(workers)

  const result = {
    succeeded: [],
    failed: [],
    errors: [],
    firstError: null
  }
  for (const outcome of outcomes) {
    if (outcome.ok) {
      result.succeeded.push(outcome.id)
      continue
    }
    result.failed.push(outcome.id)
    result.errors.push({ id: outcome.id, error: outcome.error })
    if (result.firstError === null) result.firstError = outcome.error
  }

  await options.onSettled?.(result)
  return result
}

/** One-line summary for a bulk result, e.g. "已禁用 3 项，失败 1 项：权限不足". */
export function summarizeBulk(result, actionLabel = '处理') {
  const ok = result?.succeeded?.length ?? 0
  const bad = result?.failed?.length ?? 0
  if (!bad) return `已${actionLabel} ${ok} 项`
  const reason = errorMessage(result.firstError)
  const head = ok ? `已${actionLabel} ${ok} 项，失败 ${bad} 项` : `${actionLabel}失败 ${bad} 项`
  return reason ? `${head}：${reason}` : head
}

function errorMessage(error) {
  if (!error) return ''
  if (typeof error === 'string') return error
  return error?.response?.data?.message || error?.response?.data?.error || error?.message || ''
}
