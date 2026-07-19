import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { scrollToAndHighlight } from '../scrollHighlight'

describe('scrollToAndHighlight', () => {
  let element

  beforeEach(() => {
    // Create mock DOM element
    element = {
      scrollIntoView: vi.fn(),
      classList: {
        add: vi.fn(),
        remove: vi.fn(),
        contains: vi.fn().mockReturnValue(false)
      },
      offsetHeight: 100
    }
    // Mock requestAnimationFrame
    vi.spyOn(global, 'requestAnimationFrame').mockImplementation((cb) => cb())
    vi.spyOn(global, 'setTimeout').mockImplementation((cb) => cb())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('calls scrollIntoView with correct options', () => {
    scrollToAndHighlight(element)
    expect(element.scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'center'
    })
  })

  it('adds highlight class', () => {
    scrollToAndHighlight(element)
    expect(element.classList.add).toHaveBeenCalledWith('nre-id-highlight')
  })

  it('does nothing for null element', () => {
    expect(() => scrollToAndHighlight(null)).not.toThrow()
  })

  it('does nothing for element without scrollIntoView', () => {
    expect(() => scrollToAndHighlight({})).not.toThrow()
  })
})
