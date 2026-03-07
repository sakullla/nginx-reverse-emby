<template>
  <div id="app">
    <ThemeToggle />

    <header class="header">
      <h1>✦ Nginx Reverse Proxy ✦</h1>
      <p class="subtitle">现代化反向代理管理面板</p>
    </header>

    <StatusMessage />

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

      <!-- 添加规则区域 -->
      <section class="add-rule-section">
        <div class="section-header">
          <h2>
            <span class="icon-inline" v-html="icons.plus"></span>
            新增反向代理规则
          </h2>
        </div>
        <RuleForm />
      </section>

      <!-- 规则列表区域 -->
      <section class="rules-section">
        <div class="section-header">
          <h2>
            <span class="icon-inline" v-html="icons.list"></span>
            代理规则列表
          </h2>
          <ActionBar />
        </div>
        <RuleList />
      </section>
    </main>
  </div>
</template>

<script setup>
import { onMounted, computed } from 'vue'
import { useRuleStore } from './stores/rules'
import StatusMessage from './components/StatusMessage.vue'
import RuleForm from './components/RuleForm.vue'
import ActionBar from './components/ActionBar.vue'
import RuleList from './components/RuleList.vue'
import StatCard from './components/base/StatCard.vue'
import ThemeToggle from './components/base/ThemeToggle.vue'

const ruleStore = useRuleStore()

// SVG 图标定义 (Lucide 风格)
const icons = {
  layers: '<svg viewBox="0 0 24 24"><path d="m12.83 2.18a2 2 0 0 0 -1.66 0l-7.46 3.34a2 2 0 0 0 0 3.57l7.46 3.34a2 2 0 0 0 1.66 0l7.46-3.34a2 2 0 0 0 0-3.57z"/><path d="m3.08 11.87 7.75 3.5a2 2 0 0 0 1.66 0l7.75-3.5"/><path d="m3.08 16.3 7.75 3.5a2 2 0 0 0 1.66 0l7.75-3.5"/></svg>',
  checkCircle: '<svg viewBox="0 0 24 24"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>',
  activity: '<svg viewBox="0 0 24 24"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>',
  cpu: '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M9 1v3"/><path d="M15 1v3"/><path d="M9 20v3"/><path d="M15 20v3"/><path d="M20 9h3"/><path d="M20 15h3"/><path d="M1 9h3"/><path d="M1 15h3"/></svg>',
  plus: '<svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>',
  list: '<svg viewBox="0 0 24 24"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>'
}

const activeRulesCount = computed(() => ruleStore.rules.length)

onMounted(() => {
  ruleStore.loadRules()
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
</style>
