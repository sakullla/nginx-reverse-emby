<template>
  <Teleport to="body">
    <TransitionGroup
      name="toast"
      tag="div"
      class="status-message-container"
    >
      <div
        v-for="msg in messages"
        :key="msg.id"
        class="status-message"
        :class="`status-message--${msg.type}`"
        :data-id="msg.id"
        :role="msg.type === 'error' ? 'alert' : 'status'"
        :aria-live="msg.type === 'error' ? 'assertive' : 'polite'"
        @mouseenter="(e) => pauseTimer(msg.id, e)"
        @mouseleave="(e) => resumeTimer(msg.id, e)"
      >
        <!-- 图标容器 -->
        <div class="status-message__icon-wrapper">
          <span class="status-message__icon">
            <!-- Success Icon -->
            <svg v-if="msg.type === 'success'" width="18" height="18" viewBox="0 0 24 24" fill="none">
              <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="2"/>
              <path d="M8 12l2.5 2.5L16 9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <!-- Error Icon -->
            <svg v-else-if="msg.type === 'error'" width="18" height="18" viewBox="0 0 24 24" fill="none">
              <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="2"/>
              <path d="M9 9l6 6M15 9l-6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
            <!-- Warning Icon -->
            <svg v-else-if="msg.type === 'warning'" width="18" height="18" viewBox="0 0 24 24" fill="none">
              <path d="M12 4L3 20h18L12 4z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/>
              <path d="M12 9v6M12 17h.01" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
            <!-- Info Icon -->
            <svg v-else width="18" height="18" viewBox="0 0 24 24" fill="none">
              <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="2"/>
              <path d="M12 7h.01M12 11v6" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
          </span>
        </div>

        <!-- 内容区域 -->
        <div class="status-message__content">
          <span v-if="msg.title" class="status-message__title">{{ msg.title }}</span>
          <span class="status-message__text">{{ msg.text }}</span>
        </div>

        <!-- 关闭按钮 -->
        <button class="status-message__close" aria-label="关闭" @click="remove(msg.id)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none">
            <path d="M6 6l12 12M18 6l-12 12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
          </svg>
        </button>

        <!-- 进度条 -->
        <div class="status-message__progress" :style="{ animationDuration: msg.duration + 'ms' }" aria-hidden="true"></div>
      </div>
    </TransitionGroup>
  </Teleport>
</template>

<script setup>
import { computed, onMounted } from 'vue'
import { messageStore } from '../stores/messages'

const messages = computed(() => messageStore.state.messages)

const remove = (id) => {
  messageStore.remove(id)
}

const pauseTimer = (id, event) => {
  const el = event.currentTarget.querySelector('.status-message__progress')
  if (el) {
    el.style.animationPlayState = 'paused'
  }
  messageStore.pauseTimer(id)
}

const resumeTimer = (id, event) => {
  const el = event.currentTarget.querySelector('.status-message__progress')
  if (el) {
    el.style.animationPlayState = 'running'
  }
  messageStore.resumeTimer(id)
}
</script>

<style scoped>
/* 容器 */
.status-message-container {
  position: fixed;
  top: var(--space-5);
  right: var(--space-5);
  z-index: var(--z-toast, 9999);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  pointer-events: none;
  max-width: 420px;
}

/* 消息卡片 - 毛玻璃效果（颜色跟随当前 data-theme，而非系统 prefers-color-scheme） */
.status-message {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-4);
  background: color-mix(in srgb, var(--color-bg-surface-raised) 94%, transparent);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl), var(--shadow-md);
  border: var(--border-width-thin) solid var(--color-border-default);
  pointer-events: auto;
  min-width: 320px;
  max-width: 420px;
  overflow: hidden;
  transition: all var(--duration-slow) var(--ease-default);
}

/* 顶部彩色条 */
.status-message::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
}

.status-message--success::before {
  background: linear-gradient(90deg, var(--color-success), color-mix(in srgb, var(--color-success) 65%, white));
}

.status-message--error::before {
  background: linear-gradient(90deg, var(--color-danger), color-mix(in srgb, var(--color-danger) 65%, white));
}

.status-message--warning::before {
  background: linear-gradient(90deg, var(--color-warning), color-mix(in srgb, var(--color-warning) 65%, white));
}

.status-message--info::before {
  background: linear-gradient(90deg, var(--color-primary), color-mix(in srgb, var(--color-primary) 65%, white));
}

/* 图标容器 - 圆形背景 */
.status-message__icon-wrapper {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: calc(-1 * var(--space-0-5));
}

.status-message--success .status-message__icon-wrapper {
  background: var(--color-success-subtle);
}

.status-message--error .status-message__icon-wrapper {
  background: var(--color-danger-subtle);
}

.status-message--warning .status-message__icon-wrapper {
  background: var(--color-warning-subtle);
}

.status-message--info .status-message__icon-wrapper {
  background: var(--color-primary-subtle);
}

.status-message__icon {
  display: flex;
  align-items: center;
  justify-content: center;
}

.status-message--success .status-message__icon {
  color: var(--color-success);
}

.status-message--error .status-message__icon {
  color: var(--color-danger);
}

.status-message--warning .status-message__icon {
  color: var(--color-warning);
}

.status-message--info .status-message__icon {
  color: var(--color-primary);
}

/* 内容区域 */
.status-message__content {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  padding-right: var(--space-2);
}

.status-message__title {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.4;
  letter-spacing: -0.01em;
}

.status-message__text {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  font-weight: var(--font-normal);
  line-height: 1.5;
  word-break: break-word;
  letter-spacing: 0.01em;
}

/* 关闭按钮 */
.status-message__close {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-muted);
  cursor: pointer;
  border-radius: 50%;
  transition: all var(--duration-normal) var(--ease-default);
  background: transparent;
  border: none;
  padding: 0;
  margin-top: calc(-1 * var(--space-1));
  margin-right: calc(-1 * var(--space-1));
}

.status-message__close:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
  transform: rotate(90deg);
}

.status-message__close:active {
  transform: rotate(90deg) scale(0.95);
}

/* 进度条 */
.status-message__progress {
  position: absolute;
  bottom: 0;
  left: 0;
  height: 3px;
  border-radius: 0 0 0 var(--radius-xl);
  animation: progress linear forwards;
}

.status-message--success .status-message__progress {
  background: linear-gradient(90deg, var(--color-success), color-mix(in srgb, var(--color-success) 65%, white));
}

.status-message--error .status-message__progress {
  background: linear-gradient(90deg, var(--color-danger), color-mix(in srgb, var(--color-danger) 65%, white));
}

.status-message--warning .status-message__progress {
  background: linear-gradient(90deg, var(--color-warning), color-mix(in srgb, var(--color-warning) 65%, white));
}

.status-message--info .status-message__progress {
  background: linear-gradient(90deg, var(--color-primary), color-mix(in srgb, var(--color-primary) 65%, white));
}

@keyframes progress {
  from {
    width: 100%;
  }
  to {
    width: 0%;
  }
}

/* 入场/离场动画 */
.toast-enter-active,
.toast-leave-active {
  transition: all var(--duration-slow) var(--ease-default);
}

.toast-enter-from {
  opacity: 0;
  transform: translateX(100%) scale(0.9);
}

.toast-enter-to {
  opacity: 1;
  transform: translateX(0) scale(1);
}

.toast-leave-from {
  opacity: 1;
  transform: translateX(0) scale(1);
}

.toast-leave-to {
  opacity: 0;
  transform: translateX(100%) scale(0.9);
}

.toast-move {
  transition: transform var(--duration-slow) var(--ease-default);
}

/* 悬停效果 */
.status-message:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-2xl), var(--shadow-lg);
}

/* 移动端适配 */
@media (max-width: 640px) {
  .status-message-container {
    top: auto;
    /* Keep toasts above the fixed bottom nav and the home-indicator inset. */
    bottom: calc(4rem + var(--space-3) + env(safe-area-inset-bottom, 0px));
    left: var(--space-3);
    right: var(--space-3);
    max-width: none;
  }

  .status-message {
    min-width: auto;
    max-width: 100%;
    padding: var(--space-3);
  }

  .toast-enter-from {
    opacity: 0;
    transform: translateY(100%) scale(0.95);
  }

  .toast-enter-to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }

  .toast-leave-from {
    opacity: 1;
    transform: translateY(0) scale(1);
  }

  .toast-leave-to {
    opacity: 0;
    transform: translateY(100%) scale(0.95);
  }
}

/* 减少动效偏好 */
@media (prefers-reduced-motion: reduce) {
  .status-message,
  .status-message__close,
  .toast-enter-active,
  .toast-leave-active,
  .toast-move {
    transition: none;
  }

  .status-message__progress {
    animation: none;
    display: none;
  }

  .status-message:hover {
    transform: none;
  }
}
</style>
