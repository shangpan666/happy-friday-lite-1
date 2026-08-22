import { defineStore } from 'pinia'
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
        this.notes = await enterpriseService.listNotes({ knowledgeBaseId, notebookId }) || []
      } finally {
        this.loading = false
      }
    },

    async fetchNote(noteId) {
      const note = await enterpriseService.getNote(noteId)
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
      const note = await enterpriseService.createNote({ knowledgeBaseId, notebookId, title: title || '新建笔记', content: '', contentText: '' })
      if (!note) {
        return null
      }
      this.notes.unshift(note)
      this.currentNoteId = note.id
      return note
    },

    async importNote(knowledgeBaseId, notebookId, title, content, contentText) {
      const note = await enterpriseService.createNote({ knowledgeBaseId, notebookId, title: title || '新建笔记', content: content || '', contentText: contentText || '' })
      if (!note) {
        return null
      }
      this.notes.unshift(note)
      return note
    },

    async deleteNote(noteId) {
      await enterpriseService.deleteNote(noteId)
      this.notes = this.notes.filter(n => n.id !== noteId)
      if (this.currentNoteId === noteId) {
        this.currentNoteId = this.notes.length > 0 ? this.notes[0].id : null
      }
    },

    async searchNotes(query) {
      this.loading = true
      try {
        this.notes = await enterpriseService.searchNotes(query) || []
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
        const updated = await enterpriseService.updateNote(pending.noteId, {
          title: pending.title, content: pending.content, contentText: pending.contentText,
          notebookId: note?.notebookId, knowledgeBaseId: note?.knowledgeBaseId
        })
        if (note && updated) Object.assign(note, updated)
      } finally {
        this.saving = false
      }
    }
  }
})
