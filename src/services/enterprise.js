const SERVER_KEY = 'happy-friday.server-url'
const TOKEN_KEY = 'happy-friday.access-token'
const AUTH_EVENT = 'happy-friday-auth-changed'

function normalizeUrl(value) {
  return String(value || '').trim().replace(/\/$/, '')
}

export const enterpriseService = {
  get serverUrl() {
    return normalizeUrl(localStorage.getItem(SERVER_KEY))
  },
  get token() {
    return localStorage.getItem(TOKEN_KEY) || ''
  },
  get enabled() {
    return !!(this.serverUrl && this.token)
  },
  get authenticated() {
    return this.enabled
  },
  configure(url) {
    const normalized = normalizeUrl(url)
    if (normalized) localStorage.setItem(SERVER_KEY, normalized)
    else localStorage.removeItem(SERVER_KEY)
    return normalized
  },
  logout() {
    localStorage.removeItem(TOKEN_KEY)
    window.dispatchEvent(new CustomEvent(AUTH_EVENT, { detail: { authenticated: false } }))
  },
  async request(path, options = {}) {
    if (!this.serverUrl) throw new Error('未配置企业服务地址')
    if (!this.token) throw new Error('登录已失效，请重新登录')
    const headers = { ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...(options.headers || {}) }
    if (this.token) headers.Authorization = `Bearer ${this.token}`
    let response
    try {
      response = await fetch(`${this.serverUrl}${path}`, { ...options, headers })
    } catch (error) {
      throw new Error(`无法连接服务端，请检查服务器是否运行 (${error?.message || 'network error'})`)
    }
    let payload = null
    try { payload = await response.json() } catch (_) {}
    if (!response.ok) {
      if (response.status === 401) this.logout()
      throw new Error(payload?.error || `服务请求失败 (${response.status})`)
    }
    return payload
  },
  async health(url = this.serverUrl) {
    const target = normalizeUrl(url)
    if (!target) throw new Error('请输入服务器地址')
    const response = await fetch(`${target}/health`)
    if (!response.ok) throw new Error('服务器不可用')
    return response.json()
  },
  async login(url, username, password) {
    const target = this.configure(url)
    const response = await fetch(`${target}/api/auth/login`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password })
    })
    const payload = await response.json().catch(() => null)
    if (!response.ok || !payload?.accessToken) throw new Error(payload?.error || '登录失败')
    localStorage.setItem(TOKEN_KEY, payload.accessToken)
    window.dispatchEvent(new CustomEvent(AUTH_EVENT, { detail: { authenticated: true } }))
    return payload.user
  },
  async listNotes(filters = {}) {
    const params = new URLSearchParams()
    for (const [key, value] of Object.entries(filters)) if (value) params.set(key, value)
    const suffix = params.toString() ? `?${params.toString()}` : ''
    return this.request(`/api/data/notes${suffix}`)
  },
  async getNote(id) { return this.request(`/api/data/notes/${encodeURIComponent(id)}`) },
  async createNote(note) { return this.request('/api/data/notes', { method: 'POST', body: JSON.stringify(note) }) },
  async updateNote(id, note) { return this.request(`/api/data/notes/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(note) }) },
  async deleteNote(id) { return this.request(`/api/data/notes/${encodeURIComponent(id)}`, { method: 'DELETE' }) },
  async searchNotes(query) { return this.listNotes({ q: query }) },
  async listScheduleEvents() { return this.request('/api/data/schedule-events') },
  async createScheduleEvent(event) { return this.request('/api/data/schedule-events', { method: 'POST', body: JSON.stringify(event) }) }
  ,async updateScheduleEvent(id, event) { return this.request(`/api/data/schedule-events/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(event) }) }
  ,async deleteScheduleEvent(id) { return this.request(`/api/data/schedule-events/${encodeURIComponent(id)}`, { method: 'DELETE' }) },
  async searchKnowledge(query, knowledgeBase) {
    return this.request('/api/knowledge/search', { method: 'POST', body: JSON.stringify({ query, knowledgeBase }) })
  },
  async listSessions() { return this.request('/api/data/sessions') },
  async getSessionMessages(id) { return this.request(`/api/data/messages/${encodeURIComponent(id)}`) },
  async updateSessionTitle(id, title) { return this.request(`/api/data/sessions/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ title }) }) },
  async deleteSession(id) { return this.request(`/api/data/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }) }
}
