<template>
  <div>
    <div v-if="ruleStore.loading && !ruleStore.hasRules" class="loading">
      <div class="spinner"></div>
      <p>加载中...</p>
    </div>

    <EmptyState
      v-else-if="!ruleStore.hasRules"
      icon="🎯"
      title="还没有代理规则"
      description="开始添加您的第一条反向代理规则,让流量管理变得简单高效!"
    >
      <template #action>
        <p style="color: var(--color-text-muted); font-size: var(--font-size-sm);">
          在上方表单中输入前端和后端 URL 即可添加规则
        </p>
      </template>
    </EmptyState>

    <table v-else>
      <colgroup>
        <col style="width: 60px" />
        <col />
        <col />
        <col style="width: 140px" />
      </colgroup>
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
import EmptyState from './base/EmptyState.vue'

const ruleStore = useRuleStore()
</script>
