const LOCAL_DATA_COMMANDS = new Set([
  'get_sessions', 'get_sessions_with_stats', 'get_session', 'get_session_messages',
  'save_message', 'delete_session', 'update_session_title', 'rollback_session',
  'get_notes', 'get_note', 'create_note', 'import_note', 'update_note', 'delete_note', 'search_notes',
  'get_schedule_events', 'get_schedule_events_by_date_range', 'get_schedule_event',
  'create_schedule_event', 'update_schedule_event', 'delete_schedule_event',
  'get_notebooks', 'get_notebook', 'create_notebook', 'update_notebook', 'delete_notebook',
  // Chat history is server-owned. Agent, automation and Harness remain local.
  'chat_with_memory', 'chat_without_memory', 'save_chat_history',
  // RAG vectors are server-owned; source documents remain local to the client.
  'rag-build-index', 'rag-manual-update', 'rag-get-file-status', 'rag-get-batch-status',
  'rag-get-kb-summary', 'rag-get-queue-stats', 'rag-retry-failed', 'rag-clear-kb-index', 'rag-search'
])

export const electronService = {
  async invoke(command, args) {
    // 企业版禁止业务数据回退到本地 SQLite；未迁移的能力必须明确失败。
    if (LOCAL_DATA_COMMANDS.has(command)) {
      console.error(`Local data command '${command}' is disabled in enterprise mode`)
      return null
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
