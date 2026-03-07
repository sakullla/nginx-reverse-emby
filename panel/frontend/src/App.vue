<template>
  <div id="app">
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
          icon="📋"
          variant="primary"
        />
        <StatCard
          :value="activeRulesCount"
          label="活跃规则"
          icon="✅"
          variant="success"
        />
        <StatCard
          :value="totalRequests"
          label="总请求数"
          icon="📊"
          variant="info"
        />
        <StatCard
          :value="systemStatus"
          label="系统状态"
          icon="🚀"
          variant="secondary"
        />
      </section>

      <!-- 添加规则区域 -->
      <section class="add-rule-section">
        <RuleForm />
      </section>

      <!-- 规则列表区域 -->
      <section class="rules-section">
        <div class="section-header">
          <h2>📋 代理规则列表</h2>
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

const ruleStore = useRuleStore()

// 计算活跃规则数量(这里简单地认为所有规则都是活跃的)
const activeRulesCount = computed(() => ruleStore.rules.length)

// 模拟总请求数(实际应该从后端获取)
const totalRequests = computed(() => '1.2K')

// 系统状态
const systemStatus = computed(() => '正常')

onMounted(() => {
  ruleStore.loadRules()
})
</script>
