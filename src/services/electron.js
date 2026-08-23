const LOCAL_DATA_COMMANDS = new Set([
  'get_notebooks', 'get_notebook', 'create_notebook', 'update_notebook', 'delete_notebook',
  // Chat history is server-owned. Agent, automation and Harness remain local.
  'chat_with_memory', 'chat_without_memory', 'save_chat_history',
  // RAG vectors are server-owned; source documents remain local to the client.
  'rag-build-index', 'rag-manual-update', 'rag-get-file-status', 'rag-get-batch-status',
  'rag-get-kb-summary', 'rag-get-queue-stats', 'rag-retry-failed', 'rag-clear-kb-index', 'rag-search'
])

const RAG_REMOTE = new Set(['rag-build-index', 'rag-manual-update', 'rag-get-file-status', 'rag-get-batch-status', 'rag-get-kb-summary', 'rag-get-queue-stats', 'rag-retry-failed', 'rag-clear-kb-index', 'rag-search'])

export const electronService = {
  async invoke(command, args) {
    // 企业版禁止业务数据回退到本地 SQLite；未迁移的能力必须明确失败。
    if (LOCAL_DATA_COMMANDS.has(command)) {
      if (!RAG_REMOTE.has(command)) { console.error(`Local data command '${command}' is disabled in enterprise mode`); return null }
      try {
        const { enterpriseService } = await import('./enterprise.js')
        if (command === 'rag-search') return { success: true, ...(await enterpriseService.searchKnowledge(args?.query || '', args?.kbCategoryId || '', args?.topK || 5)) }
        if (command === 'rag-get-kb-summary') return { success: true, ...(await enterpriseService.getKnowledgeSummary(args?.kbType || '')) }
        if (command === 'rag-clear-kb-index') return { success: true, ...(await enterpriseService.clearKnowledge(args?.kbType || '')) }
        if (command === 'rag-build-index') return { success: false, error: '请使用服务端索引接口提交文本块' }
        return { success: true, status: 'not-indexed', stats: {} }
      } catch (e) { console.error(`Remote RAG command '${command}' failed:`, e); return { success: false, error: e.message } }
    }
    if (window.electronAPI) {
      try {
        return await window.electronAPI.invoke(command, args)
      } catch (e) {
        console.error(`IPC invoke '${command}' failed:`, e)
        return null
      }
    }
    console.warn('Electron API not available, invoke command:', command)
    return null
  },

  async saveFile(options) {
    if (window.electronAPI) {
      return window.electronAPI.invoke('save-file-dialog', options)
    }
    console.warn('Electron API not available, using fallback')
    return null
  },

  listen(event, callback) {
    if (window.electronAPI) {
      return window.electronAPI.on(event, (data) => {
        callback({ payload: data })
      })
    }
    console.warn('Electron API not available, cannot listen to event:', event)
    return () => {}
  },

  send(channel, data) {
    if (window.electronAPI) {
      window.electronAPI.send(channel, data)
    }
  },

  get isElectron() {
    return !!window.electronAPI
  }
}
