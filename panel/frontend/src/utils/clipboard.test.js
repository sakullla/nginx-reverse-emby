import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyText, isCopyableText } from './clipboard.js'

const originalClipboard = navigator.clipboard
const originalIsSecureContext = window.isSecureContext
const originalExecCommand = document.execCommand

function setClipboard(value) {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value
  })
}

function setSecureContext(value) {
  Object.defineProperty(window, 'isSecureContext', {
    configurable: true,
    value
  })
}

afterEach(() => {
  setClipboard(originalClipboard)
  setSecureContext(originalIsSecureContext)
  document.execCommand = originalExecCommand
  vi.restoreAllMocks()
})

describe('isCopyableText', () => {
  it('returns false for empty values and placeholders', () => {
    expect(isCopyableText('')).toBe(false)
    expect(isCopyableText('   ')).toBe(false)
    expect(isCopyableText(null)).toBe(false)
    expect(isCopyableText(undefined)).toBe(false)
    expect(isCopyableText('-')).toBe(false)
    expect(isCopyableText('—')).toBe(false)
    expect(isCopyableText('  —  ')).toBe(false)
  })

  it('returns true for real addresses and strings', () => {
    expect(isCopyableText('https://media.example.com')).toBe(true)
    expect(isCopyableText('2001:db8::1')).toBe(true)
    expect(isCopyableText(8096)).toBe(true)
  })
})

describe('copyText', () => {
  it('uses navigator.clipboard.writeText in a secure context', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    setSecureContext(true)
    setClipboard({ writeText })

    const result = await copyText('https://emby.example.com')

    expect(result).toEqual({ ok: true })
    expect(writeText).toHaveBeenCalledWith('https://emby.example.com')
  })

  it('falls back to execCommand copy when clipboard API is unavailable or context is insecure', async () => {
    setSecureContext(false)
    setClipboard({ writeText: vi.fn() })
    const execCommand = vi.fn().mockReturnValue(true)
    document.execCommand = execCommand

    const result = await copyText('10.0.0.12:8096')

    expect(result).toEqual({ ok: true })
    expect(execCommand).toHaveBeenCalledWith('copy')
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('falls back to execCommand when clipboard.writeText rejects', async () => {
    setSecureContext(true)
    setClipboard({ writeText: vi.fn().mockRejectedValue(new Error('permission denied')) })
    const execCommand = vi.fn().mockReturnValue(true)
    document.execCommand = execCommand

    const result = await copyText('relay.example.com:8443')

    expect(result).toEqual({ ok: true })
    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('returns failure result without throwing when both clipboard API and fallback fail', async () => {
    setSecureContext(true)
    setClipboard({ writeText: vi.fn().mockRejectedValue(new Error('clipboard blocked')) })
    document.execCommand = vi.fn().mockReturnValue(false)

    const result = await copyText('https://example.com')

    expect(result.ok).toBe(false)
    expect(result.error).toContain('clipboard blocked')
  })

  it('returns failure for empty input without calling clipboard APIs', async () => {
    const writeText = vi.fn()
    setSecureContext(true)
    setClipboard({ writeText })

    const result = await copyText('   ')

    expect(result.ok).toBe(false)
    expect(writeText).not.toHaveBeenCalled()
  })
})
