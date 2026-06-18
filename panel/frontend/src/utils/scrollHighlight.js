/**
 * scrollHighlight — 滚动到目标元素并短暂高亮
 *
 * 用于 #id= 精确匹配定位后，将目标行滚动到可见区域并添加高亮动画。
 */

const HIGHLIGHT_CLASS = 'nre-id-highlight'
const HIGHLIGHT_DURATION_MS = 1500

// 注入高亮动画样式（仅一次）
let styleInjected = false
function ensureStyle() {
  if (styleInjected || typeof document === 'undefined') return
  const style = document.createElement('style')
  style.textContent = `
.${HIGHLIGHT_CLASS} {
  transition: background-color ${HIGHLIGHT_DURATION_MS}ms ease-out;
  background-color: rgba(255, 235, 59, 0.4) !important;
}
.${HIGHLIGHT_CLASS}.nre-id-highlight-fade {
  background-color: transparent !important;
}
`
  document.head.appendChild(style)
  styleInjected = true
}

/**
 * 滚动到目标元素并添加短暂高亮
 * @param {HTMLElement|null} element - 目标 DOM 元素
 * @param {object} [options]
 * @param {number} [options.duration] - 高亮持续时间（ms），默认 1500
 * @param {string} [options.behavior] - 滚动行为，默认 'smooth'
 */
export function scrollToAndHighlight(element, options = {}) {
  if (!element || typeof element.scrollIntoView !== 'function') return

  ensureStyle()

  const duration = options.duration || HIGHLIGHT_DURATION_MS

  // 滚动到可见区域
  element.scrollIntoView({
    behavior: options.behavior || 'smooth',
    block: 'center',
  })

  // 移除之前的高亮（如果有）
  element.classList.remove(HIGHLIGHT_CLASS, 'nre-id-highlight-fade')

  // 触发 reflow 确保 transition 生效
  void element.offsetHeight

  // 添加高亮
  element.classList.add(HIGHLIGHT_CLASS)

  // 淡出
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      element.classList.add('nre-id-highlight-fade')
    })
  })

  // 清理 class
  setTimeout(() => {
    element.classList.remove(HIGHLIGHT_CLASS, 'nre-id-highlight-fade')
  }, duration + 100)
}
