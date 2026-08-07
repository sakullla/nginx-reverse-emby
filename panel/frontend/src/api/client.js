import axios from 'axios'
import {
  clearCredentials,
  getStoredAuthToken,
  getStoredSessionToken
} from './authState'

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
  if (typeof FormData !== 'undefined' && config.data instanceof FormData) {
    if (typeof headers.delete === 'function') {
      headers.delete('Content-Type')
    } else {
      delete headers['Content-Type']
      delete headers['content-type']
    }
  }
  if (!headers.Authorization && !headers.authorization) {
    const session = getStoredSessionToken()
    if (session) {
      headers.Authorization = `Bearer ${session}`
    }
  }
  if (!headers.Authorization && !headers.authorization && !headers['X-Panel-Token']) {
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
      clearCredentials()
    }
    const message = error.response?.data?.message || error.message || '请求失败'
    const details = error.response?.data?.details
    const err = new Error(details ? `${message}: ${details}` : message)
    err.response = error.response
    err.status = status
    err.code = error.response?.data?.code
    err.context = error.response?.data?.permission_context || error.response?.data?.quota_context
    return Promise.reject(err)
  }
)
