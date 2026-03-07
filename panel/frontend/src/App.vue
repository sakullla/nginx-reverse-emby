<template>
  <div id="app">
    <!-- 初始检查中不显示内容，防止闪烁 -->
    <template v-if="!ruleStore.isAuthReady">
      <div class="initial-loading">
        <div class="loader"></div>
      </div>
    </template>

    <template v-else>
      <!-- 全局状态提示 -->
      <StatusMessage />

      <!-- 鉴权遮罩 -->
      <TokenAuth v-if="!ruleStore.isAuthenticated" />

      <template v-else>
        <ThemeToggle />
        <button @click="ruleStore.logout" class="floating-logout-btn" title="退出登录">
          <svg viewBox="0 0 24 24"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4m7 14 5-5-5-5m5 5H9"/></svg>
        </button>

        <main class="container">
          <!-- 统计面板 -->
          <section class="stats-grid">
            <StatCard
              :value="ruleStore.rules.length"
              label="代理规则"
              :icon="icons.layers"
              variant="primary"
            />
            <StatCard
              :value="activeRulesCount"
              label="活跃规则"
              :icon="icons.checkCircle"
              variant="success"
            />
            <StatCard
              :value="ruleStore.stats.totalRequests"
              label="总请求数"
              :icon="icons.activity"
              variant="info"
            />
            <StatCard
              :value="ruleStore.stats.status"
              label="系统状态"
              :icon="icons.cpu"
              variant="secondary"
            />
          </section>

          <!-- 规则列表区域 -->
          <section class="rules-section">
            <div class="section-header">
              <h2>
                <span class="icon-inline" v-html="icons.list"></span>
                代理规则列表
              </h2>
              <div class="header-actions">
                <button @click="showAddModal = true" class="add-rule-btn primary">
                  <span class="icon-inline" v-html="icons.plus"></span>
                  <span class="btn-text">添加规则</span>
                </button>
                <ActionBar />
              </div>
            </div>
            <RuleList />
          </section>

          <!-- 添加规则弹窗 -->
          <Teleport to="body">
            <BaseModal
              v-model="showAddModal"
              title="新增反向代理规则"
              :show-default-footer="false"
            >
              <RuleForm @success="showAddModal = false" />
            </BaseModal>
          </Teleport>
        </main>
      </template>
    </template>
  </div>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { useRuleStore } from './stores/rules'
import StatusMessage from './components/StatusMessage.vue'
import RuleForm from './components/RuleForm.vue'
import ActionBar from './components/ActionBar.vue'
import RuleList from './components/RuleList.vue'
import StatCard from './components/base/StatCard.vue'
import ThemeToggle from './components/base/ThemeToggle.vue'
import TokenAuth from './components/base/TokenAuth.vue'
import BaseModal from './components/base/BaseModal.vue'

const ruleStore = useRuleStore()
const showAddModal = ref(false)

// SVG 图标定义
const icons = {
  layers: '<svg viewBox="0 0 24 24"><path d="m12.83 2.18a2 2 0 0 0 -1.66 0l-7.46 3.34a2 2 0 0 0 0 3.57l7.46 3.34a2 2 0 0 0 1.66 0l7.46-3.34a2 2 0 0 0 0-3.57z"/><path d="m3.08 11.87 7.75 3.5a2 2 0 0 0 1.66 0l7.75-3.5"/><path d="m3.08 16.3 7.75 3.5a2 2 0 0 0 1.66 0l7.75-3.5"/></svg>',
  checkCircle: '<svg viewBox="0 0 24 24"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>',
  activity: '<svg viewBox="0 0 24 24"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>',
  cpu: '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M9 1v3"/><path d="M15 1v3"/><path d="M9 20v3"/><path d="M15 20v3"/><path d="M20 9h3"/><path d="M20 15h3"/><path d="M1 9h3"/><path d="M1 15h3"/></svg>',
  plus: '<svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>',
  list: '<svg viewBox="0 0 24 24"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>'
}

const activeRulesCount = computed(() => ruleStore.rules.length)

onMounted(async () => {
  await ruleStore.checkAuth()
  if (ruleStore.isAuthenticated) {
    ruleStore.loadRules()
  }
})
</script>

<style>
.icon-inline svg {
  width: 20px;
  height: 20px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
  vertical-align: text-bottom;
  margin-right: 4px;
}

.floating-logout-btn {
  position: fixed;
  top: var(--spacing-lg);
  right: calc(var(--spacing-lg) + 48px + 10px);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  background: var(--glass-bg);
  backdrop-filter: blur(var(--blur-md));
  color: var(--color-text-secondary);
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-full);
  cursor: pointer;
  z-index: var(--z-fixed, 100);
  box-shadow: var(--shadow-md);
  transition: all var(--transition-base);
}

.floating-logout-btn:hover {
  background: var(--color-danger-bg);
  color: var(--color-danger);
  border-color: var(--color-danger);
  transform: scale(1.1);
  box-shadow: var(--shadow-lg);
}

.floating-logout-btn svg {
  width: 20px;
  height: 20px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.container {
  margin-top: 80px !important;
}

@media (max-width: 768px) {
  .floating-logout-btn {
    top: var(--spacing-md);
    right: calc(var(--spacing-md) + 40px + 8px);
    width: 40px;
    height: 40px;
  }

  .container {
    margin-top: 60px !important;
  }
}

/* 初始加载状态 */
.initial-loading {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-primary);
  z-index: 9999;
}

.loader {
  width: 48px;
  height: 48px;
  border: 5px solid var(--color-border);
  border-bottom-color: var(--color-primary);
  border-radius: 50%;
  display: inline-block;
  box-sizing: border-box;
  animation: rotation 1s linear infinite;
}

@keyframes rotation {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}

.header-actions {
  display: flex;
  align-items: center;
  gap: var(--spacing-md);
}

.add-rule-btn {
  height: 40px;
  padding: 0 var(--spacing-lg);
  font-weight: var(--font-weight-semibold);
  font-size: var(--font-size-sm);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border-radius: var(--radius-md);
  transition: all var(--transition-base);
}

.btn-text {
  display: inline;
}

@media (max-width: 480px) {
  .header-actions {
    gap: var(--spacing-sm);
  }

  .add-rule-btn {
    width: 40px;
    height: 40px;
    padding: 0;
    justify-content: center;
  }

  .add-rule-btn .icon-inline {
    margin-right: 0;
  }

  .add-rule-btn .btn-text {
    display: none;
  }
}
</style>
