<template>
  <div id="app">
    <header class="header">
      <h1>✦ Nginx Reverse Proxy ✦</h1>
      <p class="subtitle">反向代理管理面板</p>
    </header>

    <StatusMessage />

    <main class="container">
      <section class="add-rule-section">
        <RuleForm />
      </section>

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
import { onMounted } from 'vue'
import { useRuleStore } from './stores/rules'
import StatusMessage from './components/StatusMessage.vue'
import RuleForm from './components/RuleForm.vue'
import ActionBar from './components/ActionBar.vue'
import RuleList from './components/RuleList.vue'

const ruleStore = useRuleStore()

onMounted(() => {
  ruleStore.loadRules()
})
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Roboto', 'Oxygen', 'Ubuntu', 'Cantarell', sans-serif;
  background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 50%, #d946ef 100%);
  min-height: 100vh;
  padding: 1.5rem;
}

#app {
  max-width: 1400px;
  margin: 0 auto;
}

.header {
  text-align: center;
  color: white;
  margin-bottom: 2rem;
  text-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
}

.header h1 {
  font-size: 2.25rem;
  font-weight: 700;
  margin-bottom: 0.5rem;
  letter-spacing: 1px;
}

.subtitle {
  font-size: 0.95rem;
  opacity: 0.95;
  font-weight: 400;
}

.container {
  display: grid;
  gap: 1.25rem;
}

.add-rule-section,
.rules-section {
  background: white;
  border-radius: 12px;
  padding: 1.75rem;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.08);
  backdrop-filter: blur(10px);
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 1.25rem;
  padding-bottom: 0.75rem;
  border-bottom: 1px solid #e8e8e8;
}

.section-header h2 {
  font-size: 1.25rem;
  color: #2d3748;
  font-weight: 600;
  margin: 0;
}

h2 {
  font-size: 1.25rem;
  color: #2d3748;
  margin-bottom: 1.25rem;
  font-weight: 600;
}

.input-group {
  display: grid;
  gap: 0.75rem;
  margin-bottom: 1rem;
}

.input-group.vertical {
  grid-template-columns: 1fr;
}

input[type="text"],
input[type="password"],
input:not([type]) {
  padding: 0.75rem 1rem;
  border: 2px solid #e5e7eb;
  border-radius: 8px;
  font-size: 0.9rem;
  color: #111827;
  transition: all 0.2s ease;
  background: #ffffff;
  font-family: inherit;
}

input[type="text"]:focus,
input[type="password"]:focus,
input:not([type]):focus {
  outline: none;
  border-color: #8b5cf6;
  color: #111827;
  background: #ffffff;
  box-shadow: 0 0 0 3px rgba(139, 92, 246, 0.1);
}

input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  background: #f9fafb;
  color: #6b7280;
}

button {
  padding: 0.75rem 1.25rem;
  border: none;
  border-radius: 8px;
  font-size: 0.9rem;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
  background: linear-gradient(135deg, #8b5cf6 0%, #7c3aed 100%);
  color: white;
  box-shadow: 0 2px 8px rgba(139, 92, 246, 0.3);
  font-family: inherit;
}

button:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(139, 92, 246, 0.4);
}

button:active:not(:disabled) {
  transform: translateY(0);
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

button.secondary {
  background: linear-gradient(135deg, #ec4899 0%, #db2777 100%);
  box-shadow: 0 2px 8px rgba(236, 72, 153, 0.3);
}

button.secondary:hover:not(:disabled) {
  box-shadow: 0 4px 12px rgba(236, 72, 153, 0.4);
}

button.danger {
  background: linear-gradient(135deg, #f43f5e 0%, #e11d48 100%);
  box-shadow: 0 2px 8px rgba(244, 63, 94, 0.3);
}

button.danger:hover:not(:disabled) {
  box-shadow: 0 4px 12px rgba(244, 63, 94, 0.4);
}

table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  margin-top: 0.5rem;
  table-layout: fixed;
}

thead {
  background: linear-gradient(135deg, #8b5cf6 0%, #7c3aed 100%);
  color: white;
}

th {
  padding: 0.875rem 1rem;
  text-align: left;
  font-weight: 600;
  font-size: 0.85rem;
  letter-spacing: 0.3px;
  text-transform: uppercase;
}

th:first-child {
  border-top-left-radius: 8px;
  padding-left: 1.25rem;
}

th:last-child {
  border-top-right-radius: 8px;
  padding-right: 1.25rem;
}

td {
  padding: 0.75rem 1rem;
  border-bottom: 1px solid #f0f0f0;
  font-size: 0.9rem;
  color: #4a5568;
  vertical-align: middle;
}

td:first-child {
  padding-left: 1.25rem;
  font-weight: 600;
  color: #8b5cf6;
}

td:last-child {
  padding-right: 1.25rem;
}

tbody tr {
  transition: all 0.15s ease;
  background: white;
}

tbody tr:hover {
  background-color: #faf5ff;
}

tbody tr:last-child td {
  border-bottom: none;
}

.loading,
.empty-state {
  text-align: center;
  padding: 3rem 1.5rem;
  color: #a0aec0;
  font-size: 0.95rem;
}

.spinner {
  width: 36px;
  height: 36px;
  margin: 0 auto 1rem;
  border: 3px solid #e5e7eb;
  border-top-color: #8b5cf6;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.token-badge {
  display: inline-block;
  padding: 0.5rem 1rem;
  background: linear-gradient(135deg, #a8edea 0%, #fed6e3 100%);
  border-radius: 20px;
  font-size: 0.85rem;
  color: #333;
  margin-top: 0.5rem;
}

@media (max-width: 768px) {
  body {
    padding: 1rem;
  }

  .header h1 {
    font-size: 1.75rem;
  }

  .add-rule-section,
  .rules-section {
    padding: 1.25rem;
  }

  .section-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.75rem;
  }

  .input-group {
    grid-template-columns: 1fr;
  }

  table {
    font-size: 0.8rem;
  }

  th, td {
    padding: 0.625rem 0.75rem;
  }

  th:first-child,
  td:first-child {
    padding-left: 0.75rem;
  }

  th:last-child,
  td:last-child {
    padding-right: 0.75rem;
  }
}
</style>
