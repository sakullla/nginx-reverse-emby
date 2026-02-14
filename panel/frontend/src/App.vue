<template>
  <div id="app">
    <header class="header">
      <h1>✦ Nginx Reverse Proxy ✦</h1>
      <p class="subtitle">二次元风格管理面板</p>
    </header>

    <StatusMessage />

    <div class="panel">
      <TokenConfig />
    </div>

    <div class="panel">
      <RuleForm />
    </div>

    <div class="panel">
      <ActionBar />
    </div>

    <div class="panel">
      <RuleList />
    </div>
  </div>
</template>

<script setup>
import { onMounted } from 'vue'
import { useRuleStore } from './stores/rules'
import StatusMessage from './components/StatusMessage.vue'
import TokenConfig from './components/TokenConfig.vue'
import RuleForm from './components/RuleForm.vue'
import ActionBar from './components/ActionBar.vue'
import RuleList from './components/RuleList.vue'

const ruleStore = useRuleStore()

onMounted(() => {
  ruleStore.loadRules()
})
</script>
