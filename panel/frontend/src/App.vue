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
        <div class="top-nav-actions">
          <ThemeToggle />
          <button @click="ruleStore.logout" class="logout-btn" title="退出登录">
            <svg viewBox="0 0 24 24"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4m7 14 5-5-5-5m5 5H9"/></svg>
            <span class="logout-text">退出</span>
          </button>
        </div>

        <header class="app-header">
          <div class="header-content">
            <h1 class="logo-title">
              <span class="logo-icon">✦</span>
              Nginx Reverse Proxy
              <span class="logo-icon">✦</span>
            </h1>
            <p class="logo-subtitle">现代化反向代理管理面板</p>
          </div>
        </header>

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

              <!-- 搜索框 -->
              <div class="search-box" v-if="ruleStore.hasRules">
                <span class="search-icon">
                  <svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
                </span>
                <input
                  v-model="ruleStore.searchQuery"
                  type="text"
                  placeholder="搜索地址..."
                  class="search-input"
                >
                <button v-if="ruleStore.searchQuery" @click="ruleStore.searchQuery = ''" class="clear-search">
                  ×
                </button>
              </div>

              <div class="header-actions">
                <button @click="showAddModal = true" class="add-rule-btn primary">
                  <span class="icon-inline" v-html="icons.plus"></span>
                  <span class="btn-text">添加规则</span>
                </button>
                <ActionBar />
              </div>

              <!-- 视图切换 -->
              <div class="view-switcher" v-if="ruleStore.hasRules">
                <button
                  @click="ruleStore.viewMode = 'grid'"
                  class="view-btn"
                  :class="{ active: ruleStore.viewMode === 'grid' }"
                  title="网格视图"
                >
                  <svg viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/></svg>
                  <span>网格</span>
                </button>
                <button
                  @click="ruleStore.viewMode = 'list'"
                  class="view-btn"
                  :class="{ active: ruleStore.viewMode === 'list' }"
                  title="列表视图"
                >
                  <svg viewBox="0 0 24 24"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
                  <span>列表</span>
                </button>
              </div>
            </div>
            <RuleList />
          </section>

          <!-- 添加规则弹窗 -->
          <Teleport to="body">
            <BaseModal
              v-model="showAddModal"
              title="新增反向代理规则"
              subtitle="配置前端访问 URL 和后端转发目标"
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
  list: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>'
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

.top-nav-actions {
  position: fixed;
  top: var(--spacing-lg);
  right: var(--spacing-lg);
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  z-index: var(--z-fixed, 100);
}

.logout-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 40px;
  padding: 0 var(--spacing-md);
  background: var(--glass-bg);
  backdrop-filter: blur(var(--blur-md));
  color: var(--color-text-secondary);
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-full);
  cursor: pointer;
  box-shadow: var(--shadow-sm);
  transition: all var(--transition-base);
  font-weight: var(--font-weight-medium);
  font-size: var(--font-size-sm);
}

.logout-btn:hover {
  background: var(--color-danger-bg);
  color: var(--color-danger);
  border-color: var(--color-danger-light);
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
}

.logout-btn svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.logo-icon {
  font-size: 1.5rem;
  opacity: 0.8;
  -webkit-text-fill-color: initial;
  color: var(--color-primary);
}

.logo-subtitle {
  font-size: 0.9rem;
  color: var(--color-text-tertiary);
  margin: 4px 0 0;
  font-weight: 500;
}

.app-header {
  padding: 40px 0 20px;
  text-align: center;
  animation: fadeIn var(--transition-slow);
}

.header-content {
  display: inline-block;
}

.logo-title {
  font-size: 2rem;
  font-weight: 800;
  margin: 0;
  letter-spacing: -0.025em;
  background: var(--gradient-header);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
}

.container {
  margin-top: 0 !important;
}

@media (max-width: 768px) {
  .app-header {
    padding: 30px 0 10px;
  }

  .logo-title {
    font-size: 1.5rem;
    gap: 8px;
  }

  .logo-icon {
    font-size: 1.2rem;
  }

  .logo-subtitle {
    font-size: 0.8rem;
  }

  .top-nav-actions {
    top: var(--spacing-sm);
    right: var(--spacing-sm);
    gap: 6px;
  }

  .logout-btn {
    height: 36px;
    padding: 0 var(--spacing-sm);
    gap: 4px;
  }

  .logout-text {
    display: none;
  }
}

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
  gap: var(--spacing-sm);
}

.add-rule-btn {
  height: 42px;
  padding: 0 var(--spacing-lg);
  font-weight: var(--font-weight-semibold);
  font-size: 0.875rem;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  border-radius: var(--radius-full);
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
  border: none;
  background: linear-gradient(135deg, var(--color-primary) 0%, var(--color-primary-dark) 100%);
  color: white;
  box-shadow: 0 2px 8px rgba(37, 99, 235, 0.25);
}

.add-rule-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 4px 16px rgba(37, 99, 235, 0.35);
}

.add-rule-btn:active {
  transform: translateY(0);
  box-shadow: 0 2px 8px rgba(37, 99, 235, 0.25);
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

/* 搜索框样式 */
.search-box {
  position: relative;
  flex: 1;
  max-width: 350px;
  min-width: 180px;
}

.search-icon {
  position: absolute;
  left: 14px;
  top: 50%;
  transform: translateY(-50%);
  width: 18px;
  height: 18px;
  color: var(--color-text-muted);
  pointer-events: none;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  z-index: 1;
}

.search-box:focus-within .search-icon {
  color: var(--color-primary);
  transform: translateY(-50%) scale(1.05);
}

.search-icon svg {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
}

.search-input {
  width: 100%;
  height: 42px;
  padding: 0 40px 0 44px;
  background: var(--color-bg-secondary);
  border: 1.5px solid transparent;
  border-radius: var(--radius-full);
  font-size: 0.875rem;
  color: var(--color-text-primary);
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  font-weight: 500;
}

.search-input::placeholder {
  color: var(--color-text-muted);
  transition: color 0.2s ease;
}

.search-input:hover {
  background: var(--color-bg-card);
  border-color: var(--color-border-light);
}

.search-input:focus {
  border-color: var(--color-primary);
  background: var(--color-bg-card);
  box-shadow: 0 0 0 4px var(--color-primary-lighter);
  outline: none;
}

.search-input:focus::placeholder {
  color: var(--color-text-disabled);
}

.clear-search {
  position: absolute;
  right: 10px;
  top: 50%;
  transform: translateY(-50%);
  background: var(--color-text-muted);
  border: none;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  cursor: pointer;
  font-size: 0.9rem;
  font-weight: 600;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  opacity: 0.7;
}

.clear-search:hover {
  opacity: 1;
  background: var(--color-danger);
  transform: translateY(-50%) scale(1.1) rotate(90deg);
}

.clear-search:active {
  transform: translateY(-50%) scale(0.9);
}

/* 视图切换样式 */
.view-switcher {
  display: flex;
  background: var(--color-bg-secondary);
  padding: 4px;
  border-radius: var(--radius-full);
  gap: 4px;
  border: none;
}

.view-btn {
  padding: 0;
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  border-radius: var(--radius-full);
  cursor: pointer;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
}

.view-btn span {
  display: none;
}

.view-btn svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2;
  fill: none;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
}

.view-btn:hover:not(.active) {
  background: var(--color-bg-card);
  color: var(--color-text-primary);
}

.view-btn:hover svg {
  transform: scale(1.1);
}

.view-btn.active {
  background: white;
  color: var(--color-primary);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08), 0 1px 2px rgba(0, 0, 0, 0.04);
}

.view-btn.active svg {
  stroke-width: 2.2;
}

.view-btn.active:hover {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1), 0 2px 4px rgba(0, 0, 0, 0.06);
}

@media (max-width: 768px) {
  .search-box {
    max-width: 42px;
    min-width: 42px;
    order: 2;
  }

  .search-box:focus-within {
    max-width: 240px;
    min-width: 180px;
  }

  .header-actions {
    order: 3;
    gap: 6px;
  }

  .view-switcher {
    order: 4;
  }

  .section-header h2 {
    order: 1;
    width: auto;
    font-size: 1.125rem;
  }
}

@media (max-width: 480px) {
  .section-header {
    gap: var(--spacing-sm);
  }

  .section-header h2 {
    width: 100%;
    font-size: 1rem;
  }

  .add-rule-btn {
    width: 42px;
    height: 42px;
    padding: 0;
    justify-content: center;
  }

  .add-rule-btn .icon-inline {
    margin-right: 0;
  }

  .add-rule-btn .btn-text {
    display: none;
  }

  .view-switcher {
    padding: 3px;
  }

  .view-btn {
    width: 34px;
    height: 34px;
  }

  .view-btn svg {
    width: 16px;
    height: 16px;
  }
}
</style>
