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
  const selectedTags = ref([]);
  const viewMode = ref(localStorage.getItem("rule_view_mode") || "grid");

  // 鉴权相关
  const token = ref(localStorage.getItem("panel_token") || "");
  const isAuthenticated = ref(false);
  const isAuthReady = ref(false);

  const hasRules = computed(() => rules.value.length > 0);

  const allTags = computed(() => {
    const tags = rules.value.flatMap((r) => r.tags || []);
    return [...new Set(tags)].sort();
  });

  const filteredRules = computed(() => {
    let result = rules.value;

    if (selectedTags.value.length > 0) {
      result = result.filter((r) =>
        selectedTags.value.some(tag => r.tags?.includes(tag))
      );
    }

    if (searchQuery.value) {
      const query = searchQuery.value.toLowerCase();
      result = result.filter(
        (rule) =>
          rule.frontend_url.toLowerCase().includes(query) ||
          rule.backend_url.toLowerCase().includes(query) ||
          String(rule.id).includes(query),
      );
    }

    return result;
  });

  async function checkAuth() {
    if (!token.value) {
      isAuthenticated.value = false;
      isAuthReady.value = true;
      return;
    }
    try {
      const ok = await api.verifyToken(token.value);
      isAuthenticated.value = ok;
      if (!ok) {
        token.value = "";
        localStorage.removeItem("panel_token");
        showError("登录令牌已过期，请重新登录");
      }
    } catch (err) {
      isAuthenticated.value = false;
      token.value = "";
      localStorage.removeItem("panel_token");
      if (err.message.includes("401")) {
        showError("会话已过期，请重新登录");
      }
    } finally {
      isAuthReady.value = true;
    }
  }

  async function login(inputToken) {
    loading.value = true;
    try {
      const ok = await api.verifyToken(inputToken);
      if (ok) {
        token.value = inputToken;
        isAuthenticated.value = true;
        localStorage.setItem("panel_token", inputToken);
        showSuccess("登录成功");
        await loadRules();
      }
    } catch (err) {
      showError("Token 无效或连接失败");
      throw err;
    } finally {
      loading.value = false;
    }
  }

  function logout() {
    token.value = "";
    isAuthenticated.value = false;
    localStorage.removeItem("panel_token");
    rules.value = [];
    showInfo("已退出登录");
  }

  async function loadStats() {
    try {
      stats.value = await api.fetchStats();
    } catch (err) {
      console.error("获取统计信息失败:", err);
    }
  }

  async function loadRules() {
    if (!isAuthenticated.value) return;
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

  async function addRule(
    frontend_url,
    backend_url,
    tags = [],
    enabled = true,
    proxy_redirect = true,
  ) {
    loading.value = true;
    error.value = null;
    try {
      const newRule = await api.createRule(
        frontend_url,
        backend_url,
        tags,
        enabled,
        proxy_redirect,
      );
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

  async function modifyRule(
    id,
    frontend_url,
    backend_url,
    tags,
    enabled,
    proxy_redirect,
  ) {
    loading.value = true;
    error.value = null;
    try {
      await api.updateRule(
        id,
        frontend_url,
        backend_url,
        tags,
        enabled,
        proxy_redirect,
      );
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

  function toggleViewMode() {
    viewMode.value = viewMode.value === "grid" ? "list" : "grid";
    localStorage.setItem("rule_view_mode", viewMode.value);
  }

  return {
    rules,
    stats,
    searchQuery,
    selectedTags,
    allTags,
    viewMode,
    filteredRules,
    loading,
    error,
    statusMessage,
    hasRules,
    isAuthenticated,
    isAuthReady,
    token,
    checkAuth,
    login,
    logout,
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
    toggleViewMode,
  };
});
