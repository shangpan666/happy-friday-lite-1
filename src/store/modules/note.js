import { defineStore } from 'pinia'
import { electronService } from '@/services/electron'
import { enterpriseService } from '@/services/enterprise'

const DEBOUNCE_MS = 800

export const useNoteStore = defineStore('note', {
  state: () => ({
    notes: [],
    currentNoteId: null,
    loading: false,
    saving: false,
    _saveTimer: null,
    _pendingSave: null
  }),

  getters: {
    currentNote(state) {
      return state.notes.find(n => n.id === state.currentNoteId)
    }
  },

  actions: {
    async fetchNotes(knowledgeBaseId, notebookId) {
      this.loading = true
      try {
        if (enterpriseService.enabled) {
          this.notes = await enterpriseService.listNotes() || []
          return
        }
        const notes = await electronService.invoke('get_notes', {
          knowledgeBaseId: knowledgeBaseId ?? null,
          notebookId: notebookId ?? null
        })
        this.notes = notes || []
      } finally {
        this.loading = false
      }
    },

    async fetchNote(noteId) {
      if (enterpriseService.enabled) {
        return this.notes.find(n => n.id === noteId) || null
      }
      const note = await electronService.invoke('get_note', { noteId })
      if (note) {
        const idx = this.notes.findIndex(n => n.id === noteId)
        if (idx >= 0) {
          this.notes[idx] = note
        } else {
          this.notes.unshift(note)
        }
      }
      return note
    },

    async createNote(knowledgeBaseId, title, notebookId) {
      if (enterpriseService.enabled) {
        const note = await enterpriseService.createNote({ title: title || '新建笔记', content: '', contentText: '' })
        this.notes.unshift(note); this.currentNoteId = note.id; return note
      }
      const note = await electronService.invoke('create_note', {
        knowledgeBaseId: knowledgeBaseId ?? null,
        notebookId: notebookId ?? null,
        title: title ?? null
      })
      if (!note) {
        return null
      }
      this.notes.unshift(note)
      this.currentNoteId = note.id
      return note
    },

    async importNote(knowledgeBaseId, notebookId, title, content, contentText) {
      if (enterpriseService.enabled) {
        const note = await enterpriseService.createNote({ title: title || '新建笔记', content: content || '', contentText: contentText || '' })
        if (note) this.notes.unshift(note)
        return note
      }
      const note = await electronService.invoke('import_note', {
        knowledgeBaseId: knowledgeBaseId ?? null,
        notebookId: notebookId ?? null,
        title: title ?? null,
        content: content ?? '',
        contentText: contentText ?? ''
      })
      if (!note) {
        return null
      }
      this.notes.unshift(note)
      return note
    },

    async deleteNote(noteId) {
      if (enterpriseService.enabled) {
        await enterpriseService.deleteNote(noteId)
        this.notes = this.notes.filter(n => n.id !== noteId)
        return
      }
      await electronService.invoke('delete_note', { noteId })
      this.notes = this.notes.filter(n => n.id !== noteId)
      if (this.currentNoteId === noteId) {
        this.currentNoteId = this.notes.length > 0 ? this.notes[0].id : null
      }
    },

    async searchNotes(query) {
      this.loading = true
      try {
        if (enterpriseService.enabled) {
          const notes = await enterpriseService.listNotes()
          const q = String(query || '').toLowerCase()
          this.notes = (notes || []).filter(n => !q || `${n.title} ${n.contentText}`.toLowerCase().includes(q))
          return
        }
        const notes = await electronService.invoke('search_notes', { query })
        this.notes = notes || []
      } finally {
        this.loading = false
      }
    },

    selectNote(noteId) {
      this.currentNoteId = noteId
    },

    scheduleSave(noteId, title, content, contentText) {
      this._pendingSave = { noteId, title, content, contentText }

      if (this._saveTimer) {
        clearTimeout(this._saveTimer)
      }

      this._saveTimer = setTimeout(() => {
        this._flushSave()
      }, DEBOUNCE_MS)
    },

    async flushPendingSave() {
      if (this._saveTimer) {
        clearTimeout(this._saveTimer)
        this._saveTimer = null
      }
      if (this._pendingSave) {
        await this._flushSave()
      }
    },

    async _flushSave() {
      this._saveTimer = null
      const pending = this._pendingSave
      if (!pending) return

      this._pendingSave = null
      this.saving = true
      try {
        const note = this.notes.find(n => n.id === pending.noteId)
        if (enterpriseService.enabled) {
          const updated = await enterpriseService.updateNote(pending.noteId, {
            title: pending.title, content: pending.content, contentText: pending.contentText
          })
          if (note && updated) Object.assign(note, updated, { updatedAt: new Date().toISOString() })
          return
        }
        const notebookId = note?.notebookId ?? null

        await electronService.invoke('update_note', {
          noteId: pending.noteId,
          title: pending.title,
          content: pending.content,
          contentText: pending.contentText,
          notebookId
        })

        if (note) {
          note.title = pending.title
          note.content = pending.content
          note.contentText = pending.contentText
          note.updatedAt = new Date().toISOString()
        }
      } finally {
        this.saving = false
      }
    }
  }
})
