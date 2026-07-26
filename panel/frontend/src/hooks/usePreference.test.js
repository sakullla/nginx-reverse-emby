import { beforeEach, describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import { usePreference, __resetPreferenceCacheForTests } from './usePreference'

describe('usePreference', () => {
  beforeEach(() => {
    localStorage.clear()
    __resetPreferenceCacheForTests()
  })

  it('falls back to default when nothing stored', () => {
    const value = usePreference('test-default-' + Math.random(), 'nodes')
    expect(value.value).toBe('nodes')
  })

  it('reads previously stored value', () => {
    const key = 'test-read-' + Math.random()
    localStorage.setItem('pref:' + key, JSON.stringify('business'))
    const value = usePreference(key, 'nodes')
    expect(value.value).toBe('business')
  })

  it('persists changes to localStorage', async () => {
    const key = 'test-write-' + Math.random()
    const value = usePreference(key, 'nodes')
    value.value = 'business'
    await nextTick()
    expect(JSON.parse(localStorage.getItem('pref:' + key))).toBe('business')
  })

  it('shares the same ref across callers of one key', () => {
    const key = 'test-share-' + Math.random()
    const a = usePreference(key, 'nodes')
    const b = usePreference(key, 'nodes')
    expect(a).toBe(b)
  })
})
