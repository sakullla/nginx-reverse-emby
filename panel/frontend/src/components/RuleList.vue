<template>
  <div>
    <h2>📋 代理规则列表</h2>
    
    <div v-if="ruleStore.loading && !ruleStore.hasRules" class="loading">
      <div class="spinner"></div>
      <p>加载中...</p>
    </div>

    <div v-else-if="!ruleStore.hasRules" class="empty-state">
      暂无规则，快来添加第一条吧！
    </div>

    <table v-else>
      <thead>
        <tr>
          <th>ID</th>
          <th>前端 URL</th>
          <th>后端 URL</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <RuleItem
          v-for="rule in ruleStore.rules"
          :key="rule.id"
          :rule="rule"
        />
      </tbody>
    </table>
  </div>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'
import RuleItem from './RuleItem.vue'

const ruleStore = useRuleStore()
</script>
