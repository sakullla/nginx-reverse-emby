const PLACEHOLDER_VALUES = new Set(['-', '—', '--', '–', 'N/A'])

export function isCopyableText(value) {
  if (value === null || value === undefined) return false
  const text = String(value).trim()
  if (!text) return false
  return !PLACEHOLDER_VALUES.has(text)
}

function fallbackCopyText(text) {
  if (typeof document === 'undefined' || !document.body || typeof document.createElement !== 'function') {
    return { ok: false, error: '当前环境不支持复制' }
  }
  if (typeof document.execCommand !== 'function') {
    return { ok: false, error: '当前环境不支持复制' }
  }

  const textarea = document.createElement('textarea')
  const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.setAttribute('aria-hidden', 'true')
  textarea.style.position = 'fixed'
  textarea.style.top = '0'
  textarea.style.left = '-9999px'
  textarea.style.opacity = '0'
  textarea.style.pointerEvents = 'none'

  document.body.appendChild(textarea)
  try {
    textarea.focus({ preventScroll: true })
    textarea.select()
    textarea.setSelectionRange(0, text.length)
    const copied = document.execCommand('copy')
    return copied ? { ok: true } : { ok: false, error: '复制失败，请手动复制' }
  } catch (error) {
    return { ok: false, error: error?.message || '复制失败，请手动复制' }
  } finally {
    if (textarea.parentNode) {
      textarea.parentNode.removeChild(textarea)
    }
    if (previousFocus && document.contains(previousFocus)) {
      previousFocus.focus({ preventScroll: true })
    }
  }
}

export async function copyText(value) {
  if (value === null || value === undefined) {
    return { ok: false, error: '没有可复制的内容' }
  }
  const text = String(value)
  if (!text.trim()) {
    return { ok: false, error: '没有可复制的内容' }
  }

  let primaryError = ''
  const isSecure = typeof window === 'undefined' ? true : window.isSecureContext !== false
  if (isSecure && typeof navigator !== 'undefined' && typeof navigator.clipboard?.writeText === 'function') {
    try {
      await navigator.clipboard.writeText(text)
      return { ok: true }
    } catch (error) {
      primaryError = error?.message || '复制失败'
    }
  }

  const fallbackResult = fallbackCopyText(text)
  if (fallbackResult.ok) return fallbackResult
  return {
    ok: false,
    error: primaryError || fallbackResult.error || '复制失败，请手动复制'
  }
}
