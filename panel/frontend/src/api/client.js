import axios from 'axios'
import { clearAuthToken, getStoredAuthToken } from './authState'

export const api = axios.create({
  baseURL: '/panel-api',
  timeout: 10000,
  headers: {
    'Content-Type': 'application/json'
  }
})

export const longRunningRequest = {
  timeout: 0
}

api.interceptors.request.use((config) => {
  const headers = config.headers || {}
  if (!headers['X-Panel-Token']) {
    const token = getStoredAuthToken()
    if (token) {
      headers['X-Panel-Token'] = token
    }
  }
  config.headers = headers
  return config
})

api.interceptors.response.use(
  (response) => response,
  (error) => {
    const status = error.response?.status
    if (status === 401) {
      clearAuthToken()
    }
    const message = error.response?.data?.message || error.message || '请求失败'
    const details = error.response?.data?.details
    const err = new Error(details ? `${message}: ${details}` : message)
    err.response = error.response
    err.status = status
    return Promise.reject(err)
  }
)
