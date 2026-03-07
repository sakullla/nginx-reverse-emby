<template>
  <div>
    <div v-if="ruleStore.loading && !ruleStore.hasRules" class="loading">
      <div class="spinner"></div>
      <p>加载中...</p>
    </div>

    <template v-else>
      <div class="search-container" v-if="ruleStore.hasRules">
        <span class="search-icon">🔍</span>
        <input
          v-model="ruleStore.searchQuery"
          type="text"
          placeholder="搜索规则 ID、前端或后端 URL..."
          class="search-input"
        >
      </div>

      <EmptyState
        v-if="!ruleStore.hasRules"
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

      <EmptyState
        v-else-if="ruleStore.filteredRules.length === 0"
        icon="🔎"
        title="没有找到匹配的规则"
        :description="`未能找到包含 '${ruleStore.searchQuery}' 的规则。`"
      >
        <template #action>
          <button @click="ruleStore.searchQuery = ''" class="secondary small">
            重置搜索
          </button>
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
            v-for="rule in ruleStore.filteredRules"
            :key="rule.id"
            :rule="rule"
          />
        </tbody>
      </table>
    </template>
  </div>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'
import RuleItem from './RuleItem.vue'
import EmptyState from './base/EmptyState.vue'

const ruleStore = useRuleStore()
</script>
