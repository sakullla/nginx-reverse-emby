import axios from 'axios'

const api = axios.create({
  baseURL: '/panel-api',
  timeout: 10000,
  headers: {
    'Content-Type': 'application/json'
  }
})

api.interceptors.response.use(
  response => response,
  error => {
    const message = error.response?.data?.message || error.message || '请求失败'
    const details = error.response?.data?.details
    return Promise.reject(new Error(details ? `${message}: ${details}` : message))
  }
)

export async function fetchRules() {
  const { data } = await api.get('/rules')
  return data.rules || []
}

export async function createRule(frontend_url, backend_url) {
  const { data } = await api.post('/rules', { frontend_url, backend_url })
  return data.rule
}

export async function updateRule(id, frontend_url, backend_url) {
  const { data } = await api.put(`/rules/${id}`, { frontend_url, backend_url })
  return data.rule
}

export async function deleteRule(id) {
  const { data } = await api.delete(`/rules/${id}`)
  return data.rule
}

export async function applyConfig() {
  const { data } = await api.post('/apply')
  return data
}

export async function checkHealth() {
  const { data } = await api.get('/health')
  return data
}
