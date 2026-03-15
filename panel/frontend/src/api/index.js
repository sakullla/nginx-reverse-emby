import axios from "axios";

// 检测是否为开发环境
const isDev = import.meta.env.DEV;
// 模拟延迟函数
const sleep = (ms = 500) => new Promise((resolve) => setTimeout(resolve, ms));

const api = axios.create({
  baseURL: "/panel-api",
  timeout: 10000,
  headers: {
    "Content-Type": "application/json",
  },
});

const longRunningRequest = {
  timeout: 0,
};

// 请求拦截器：注入 Token
api.interceptors.request.use((config) => {
  const token = localStorage.getItem("panel_token");
  if (token) {
    config.headers["X-Panel-Token"] = token;
  }
  return config;
});

api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem("panel_token");
    }
    const message =
      error.response?.data?.message || error.message || "请求失败";
    const details = error.response?.data?.details;
    return Promise.reject(
      new Error(details ? `${message}: ${details}` : message),
    );
  },
);

// --- Mock 数据开始 ---
const mockRules = [
  {
    id: 1,
    frontend_url: "https://emby.example.com",
    backend_url: "http://192.168.1.10:8096",
    enabled: true,
    tags: ["emby", "https"],
    proxy_redirect: true,
  },
  {
    id: 2,
    frontend_url: "https://jellyfin.example.com",
    backend_url: "http://192.168.1.11:8096",
    enabled: false,
    tags: ["jellyfin"],
    proxy_redirect: true,
  },
];

const mockStats = {
  totalRequests: "8.4K",
  status: "正常 (Mock)",
};
// --- Mock 数据结束 ---

export async function verifyToken(token) {
  if (isDev) {
    await sleep();
    return token === "admin"; // 开发环境默认 admin 即可登录
  }
  const { data } = await api.get("/auth/verify", {
    headers: { "X-Panel-Token": token },
  });
  return data.ok;
}

export async function fetchStats() {
  if (isDev) {
    await sleep();
    return mockStats;
  }
  const { data } = await api.get("/stats");
  return data.stats;
}

export async function fetchRules() {
  if (isDev) {
    await sleep();
    return [...mockRules];
  }
  const { data } = await api.get("/rules");
  return data.rules || [];
}

export async function createRule(
  frontend_url,
  backend_url,
  tags = [],
  enabled = true,
  proxy_redirect = true,
) {
  if (isDev) {
    await sleep();
    const newRule = {
      id: Date.now(),
      frontend_url,
      backend_url,
      tags,
      enabled,
      proxy_redirect,
    };
    mockRules.push(newRule);
    return newRule;
  }
  const { data } = await api.post(
    "/rules",
    { frontend_url, backend_url, tags, enabled, proxy_redirect },
    longRunningRequest,
  );
  return data.rule;
}

export async function updateRule(
  id,
  frontend_url,
  backend_url,
  tags,
  enabled,
  proxy_redirect,
) {
  if (isDev) {
    await sleep();
    const idx = mockRules.findIndex((r) => r.id === id);
    if (idx !== -1) {
      const updated = { ...mockRules[idx] };
      if (frontend_url !== undefined) updated.frontend_url = frontend_url;
      if (backend_url !== undefined) updated.backend_url = backend_url;
      if (tags !== undefined) updated.tags = tags;
      if (enabled !== undefined) updated.enabled = enabled;
      if (proxy_redirect !== undefined) updated.proxy_redirect = proxy_redirect;
      mockRules[idx] = updated;
    }
    return mockRules[idx];
  }
  const { data } = await api.put(
    `/rules/${id}`,
    { frontend_url, backend_url, tags, enabled, proxy_redirect },
    longRunningRequest,
  );
  return data.rule;
}

export async function deleteRule(id) {
  if (isDev) {
    await sleep();
    const idx = mockRules.findIndex((r) => r.id === id);
    return mockRules.splice(idx, 1)[0];
  }
  const { data } = await api.delete(`/rules/${id}`, longRunningRequest);
  return data.rule;
}

export async function applyConfig() {
  if (isDev) {
    await sleep(1500);
    return { ok: true };
  }
  const { data } = await api.post("/apply", {}, longRunningRequest);
  return data;
}

export async function checkHealth() {
  if (isDev) return { ok: true };
  const { data } = await api.get("/health");
  return data;
}
