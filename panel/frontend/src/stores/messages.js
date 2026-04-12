import { reactive, readonly } from 'vue'

/**
 * 全局消息状态管理 - SaaS 标准消息提醒方案
 * 支持 success、error、info、warning 四种消息类型
 * 消息自动消失，error 类型显示 8 秒，其他类型显示 5 秒
 */

let idCounter = 0

const state = reactive({
  messages: []
})

/**
 * 添加消息
 * @param {Object} options
 * @param {string} options.type - 消息类型: 'success' | 'error' | 'info' | 'warning'
 * @param {string} options.title - 消息标题（可选）
 * @param {string} options.text - 消息内容
 * @param {number} options.duration - 显示时长（毫秒），默认 error 8000ms，其他 5000ms
 */
function addMessage({ type = 'info', title = '', text = '', duration } = {}) {
  const id = ++idCounter
  const message = {
    id,
    type,
    title: title || getDefaultTitle(type),
    text,
    duration: duration || (type === 'error' ? 8000 : 5000)
  }

  state.messages.push(message)

  // 自动移除
  setTimeout(() => {
    removeMessage(id)
  }, message.duration)

  return id
}

/**
 * 根据类型获取默认标题
 */
function getDefaultTitle(type) {
  const titles = {
    success: '操作成功',
    error: '操作失败',
    info: '提示',
    warning: '警告'
  }
  return titles[type] || '提示'
}

/**
 * 移除指定消息
 */
function removeMessage(id) {
  const index = state.messages.findIndex((m) => m.id === id)
  if (index > -1) {
    state.messages.splice(index, 1)
  }
}

/**
 * 快捷方法：显示成功消息
 */
function showSuccess(text, title = '') {
  return addMessage({ type: 'success', title, text })
}

/**
 * 快捷方法：显示错误消息
 * 支持传入 Error 对象或字符串
 */
function showError(error, title = '') {
  let text = ''
  let errorDetails = ''

  if (error instanceof Error) {
    text = error.message || '未知错误'
    // 如果有后端返回的详细信息
    if (error.response?.data) {
      const data = error.response.data
      if (data.message) text = data.message
      if (data.details) errorDetails = data.details
    }
  } else if (typeof error === 'string') {
    text = error
  } else if (error && typeof error === 'object') {
    text = error.message || JSON.stringify(error)
  }

  // 组合错误信息
  const fullText = errorDetails ? `${text}: ${errorDetails}` : text

  return addMessage({
    type: 'error',
    title: title || '操作失败',
    text: fullText
  })
}

/**
 * 快捷方法：显示信息消息
 */
function showInfo(text, title = '') {
  return addMessage({ type: 'info', title, text })
}

/**
 * 快捷方法：显示警告消息
 */
function showWarning(text, title = '') {
  return addMessage({ type: 'warning', title, text })
}

/**
 * 清空所有消息
 */
function clearAll() {
  state.messages.splice(0, state.messages.length)
}

export const messageStore = {
  state: readonly(state),
  add: addMessage,
  remove: removeMessage,
  success: showSuccess,
  error: showError,
  info: showInfo,
  warning: showWarning,
  clearAll
}

// 兼容旧版 ruleStore.statusMessage 的 API
// 用于 StatusMessage.vue 的过渡
export function useMessageStore() {
  return messageStore
}
