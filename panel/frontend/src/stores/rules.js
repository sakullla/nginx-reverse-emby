import { defineStore } from "pinia";
import { ref, computed } from "vue";
import * as api from "../api";

export const useRuleStore = defineStore("rules", () => {
  const rules = ref([]);
  const stats = ref({ totalRequests: "0", status: "未知" });
  const loading = ref(false);
  const error = ref(null);
  const statusMessage = ref(null);
  const searchQuery = ref("");

  const hasRules = computed(() => rules.value.length > 0);

  const filteredRules = computed(() => {
    if (!searchQuery.value) return rules.value;
    const query = searchQuery.value.toLowerCase();
    return rules.value.filter(
      (rule) =>
        rule.frontend_url.toLowerCase().includes(query) ||
        rule.backend_url.toLowerCase().includes(query) ||
        String(rule.id).includes(query),
    );
  });

  async function loadStats() {
    try {
      stats.value = await api.fetchStats();
    } catch (err) {
      console.error("获取统计信息失败:", err);
    }
  }

  async function loadRules() {
    loading.value = true;
    error.value = null;
    try {
      const [rulesData] = await Promise.all([api.fetchRules(), loadStats()]);
      rules.value = rulesData;
    } catch (err) {
      error.value = err.message;
      showError(err.message);
    } finally {
      loading.value = false;
    }
  }

  async function addRule(frontend_url, backend_url) {
    loading.value = true;
    error.value = null;
    try {
      const newRule = await api.createRule(frontend_url, backend_url);
      await loadRules();
      showSuccess("规则已新增");
      return newRule;
    } catch (err) {
      error.value = err.message;
      showError(err.message);
      throw err;
    } finally {
      loading.value = false;
    }
  }

  async function modifyRule(id, frontend_url, backend_url) {
    loading.value = true;
    error.value = null;
    try {
      await api.updateRule(id, frontend_url, backend_url);
      await loadRules();
      showSuccess(`规则 ${id} 已更新`);
    } catch (err) {
      error.value = err.message;
      showError(err.message);
      throw err;
    } finally {
      loading.value = false;
    }
  }

  async function removeRule(id) {
    loading.value = true;
    error.value = null;
    try {
      await api.deleteRule(id);
      await loadRules();
      showSuccess(`规则 ${id} 已删除`);
    } catch (err) {
      error.value = err.message;
      showError(err.message);
      throw err;
    } finally {
      loading.value = false;
    }
  }

  async function applyNginxConfig() {
    loading.value = true;
    error.value = null;
    try {
      await api.applyConfig();
      showSuccess("Nginx 配置已应用并重载");
    } catch (err) {
      error.value = err.message;
      showError(err.message);
      throw err;
    } finally {
      loading.value = false;
    }
  }

  function showSuccess(message) {
    statusMessage.value = { type: "success", text: message };
    setTimeout(() => {
      statusMessage.value = null;
    }, 5000);
  }

  function showError(message) {
    statusMessage.value = { type: "error", text: message };
    setTimeout(() => {
      statusMessage.value = null;
    }, 8000);
  }

  function showInfo(message) {
    statusMessage.value = { type: "info", text: message };
    setTimeout(() => {
      statusMessage.value = null;
    }, 5000);
  }

  function clearStatus() {
    statusMessage.value = null;
  }

  return {
    rules,
    stats,
    searchQuery,
    filteredRules,
    loading,
    error,
    statusMessage,
    hasRules,
    loadRules,
    loadStats,
    addRule,
    modifyRule,
    removeRule,
    applyNginxConfig,
    showSuccess,
    showError,
    showInfo,
    clearStatus,
  };
});
