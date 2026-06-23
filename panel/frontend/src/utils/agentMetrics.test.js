import { describe, expect, it } from 'vitest'
import { barTone, bytesPair, clamp, cpuUsage, formatBytes, percent, rate } from './agentMetrics.js'

describe('agentMetrics', () => {
  describe('percent', () => {
    it('formats finite numbers to one decimal', () => {
      expect(percent(12.4)).toBe('12.4%')
      expect(percent(0)).toBe('0.0%')
      expect(percent(100)).toBe('100.0%')
    })

    it('returns placeholder for missing or invalid values', () => {
      expect(percent(null)).toBe('—')
      expect(percent(undefined)).toBe('—')
      expect(percent('')).toBe('—')
      expect(percent(NaN)).toBe('—')
    })
  })

  describe('clamp', () => {
    it('clamps values to [0, 100]', () => {
      expect(clamp(50)).toBe(50)
      expect(clamp(-10)).toBe(0)
      expect(clamp(150)).toBe(100)
    })

    it('returns 0 for missing or invalid values', () => {
      expect(clamp(null)).toBe(0)
      expect(clamp(undefined)).toBe(0)
      expect(clamp('')).toBe(0)
      expect(clamp(NaN)).toBe(0)
    })
  })

  describe('barTone', () => {
    it('maps thresholds to semantic tones', () => {
      expect(barTone(0)).toBe('success')
      expect(barTone(69)).toBe('success')
      expect(barTone(70)).toBe('warning')
      expect(barTone(85)).toBe('warning')
      expect(barTone(86)).toBe('danger')
    })

    it('returns neutral for invalid values', () => {
      expect(barTone(null)).toBe('neutral')
      expect(barTone(NaN)).toBe('neutral')
    })
  })

  describe('formatBytes', () => {
    it('uses binary units', () => {
      expect(formatBytes(0)).toBe('0 B')
      expect(formatBytes(1024)).toBe('1.00 KiB')
      expect(formatBytes(1024 * 1024)).toBe('1.00 MiB')
      expect(formatBytes(1024 * 1024 * 1024)).toBe('1.00 GiB')
    })
  })

  describe('rate', () => {
    it('formats bytes per second', () => {
      expect(rate(1024)).toBe('1.00 KiB/s')
      expect(rate(0)).toBe('0 B/s')
    })

    it('returns placeholder for invalid values', () => {
      expect(rate(null)).toBe('—')
      expect(rate(NaN)).toBe('—')
    })
  })

  describe('cpuUsage', () => {
    it('shows used / total cores when available', () => {
      expect(cpuUsage({ cpu_used_cores: 1, cpu_total_cores: 8 })).toBe('1.0 / 8 核')
    })

    it('falls back to usage percent', () => {
      expect(cpuUsage({ cpu_usage_percent: 12.4 })).toBe('12.4%')
    })

    it('returns placeholder when nothing is available', () => {
      expect(cpuUsage({})).toBe('—')
    })
  })

  describe('bytesPair', () => {
    it('shows used / total when available', () => {
      expect(bytesPair(1024 * 1024 * 1024 * 10, 1024 * 1024 * 1024 * 16)).toBe('10.0 GiB / 16.0 GiB')
    })

    it('shows only used when total is missing', () => {
      expect(bytesPair(1024 * 1024 * 1024 * 10, null)).toBe('10.0 GiB')
    })

    it('returns placeholder when used is missing', () => {
      expect(bytesPair(null, 1024 * 1024 * 1024 * 16)).toBe('—')
    })
  })
})
