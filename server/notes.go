package main

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

// notesAPI owns the complete server-side note lifecycle. Every query includes
// user_id so a token can only access its own notes.
func (a *App) notesAPI(w http.ResponseWriter, r *http.Request, uid int64, id string) {
	switch {
	case r.Method == http.MethodGet && id == "":
		a.listNotes(w, r, uid)
	case r.Method == http.MethodGet && id != "":
		a.getNote(w, uid, id)
	case r.Method == http.MethodPost && id == "":
		a.createNote(w, r, uid)
	case r.Method == http.MethodPut && id != "":
		a.updateNote(w, r, uid, id)
	case r.Method == http.MethodDelete && id != "":
		a.deleteNote(w, uid, id)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) listNotes(w http.ResponseWriter, r *http.Request, uid int64) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	query := `SELECT id,knowledge_base_id,notebook_id,title,content,content_text,created_at,updated_at
		FROM notes WHERE user_id=? AND deleted=0`
	args := []any{uid}
	if q != "" {
		query += " AND (LOWER(title) LIKE ? OR LOWER(content_text) LIKE ?)"
		like := "%" + strings.ToLower(q) + "%"
		args = append(args, like, like)
	}
	if notebookID := strings.TrimSpace(r.URL.Query().Get("notebookId")); notebookID != "" {
		query += " AND notebook_id=?"
		args = append(args, notebookID)
	}
	if knowledgeBaseID := strings.TrimSpace(r.URL.Query().Get("knowledgeBaseId")); knowledgeBaseID != "" {
		query += " AND knowledge_base_id=?"
		args = append(args, knowledgeBaseID)
	}
	query += " ORDER BY updated_at DESC"
	rows, err := a.db.Query(query, args...)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, content, contentText, created, updated string
		var kbID, notebookID sql.NullString
		if rows.Scan(&id, &kbID, &notebookID, &title, &content, &contentText, &created, &updated) == nil {
			out = append(out, noteJSON(id, kbID.String, notebookID.String, title, content, contentText, created, updated))
		}
	}
	jsonOut(w, http.StatusOK, out)
}

func (a *App) getNote(w http.ResponseWriter, uid int64, id string) {
	var title, content, contentText, created, updated string
	var kbID, notebookID sql.NullString
	err := a.db.QueryRow(`SELECT knowledge_base_id,notebook_id,title,content,content_text,created_at,updated_at
		FROM notes WHERE id=? AND user_id=? AND deleted=0`, id, uid).
		Scan(&kbID, &notebookID, &title, &content, &contentText, &created, &updated)
	if err == sql.ErrNoRows {
		jsonOut(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, http.StatusOK, noteJSON(id, kbID.String, notebookID.String, title, content, contentText, created, updated))
}

func (a *App) createNote(w http.ResponseWriter, r *http.Request, uid int64) {
	var in struct {
		KnowledgeBaseID string `json:"knowledgeBaseId"`
		NotebookID      string `json:"notebookId"`
		Title           string `json:"title"`
		Content         string `json:"content"`
		ContentText     string `json:"contentText"`
	}
	if !readJSON(r, &in) {
		jsonOut(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if in.Title == "" {
		in.Title = "新建笔记"
	}
	now, id := time.Now().UTC().Format(time.RFC3339), randomToken()
	_, err := a.db.Exec(`INSERT INTO notes(id,user_id,knowledge_base_id,notebook_id,title,content,content_text,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, id, uid, nullable(in.KnowledgeBaseID), nullable(in.NotebookID), in.Title, in.Content, in.ContentText, now, now)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, http.StatusCreated, noteJSON(id, in.KnowledgeBaseID, in.NotebookID, in.Title, in.Content, in.ContentText, now, now))
}

func (a *App) updateNote(w http.ResponseWriter, r *http.Request, uid int64, id string) {
	var in struct {
		KnowledgeBaseID *string `json:"knowledgeBaseId"`
		NotebookID      *string `json:"notebookId"`
		Title           string  `json:"title"`
		Content         string  `json:"content"`
		ContentText     string  `json:"contentText"`
	}
	if !readJSON(r, &in) {
		jsonOut(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	var currentKB, currentNotebook sql.NullString
	if err := a.db.QueryRow("SELECT knowledge_base_id,notebook_id FROM notes WHERE id=? AND user_id=? AND deleted=0", id, uid).Scan(&currentKB, &currentNotebook); err != nil {
		if err == sql.ErrNoRows {
			jsonOut(w, http.StatusNotFound, map[string]string{"error": "not found"})
		} else {
			jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	kbID, notebookID := currentKB.String, currentNotebook.String
	if in.KnowledgeBaseID != nil {
		kbID = *in.KnowledgeBaseID
	}
	if in.NotebookID != nil {
		notebookID = *in.NotebookID
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := a.db.Exec(`UPDATE notes SET knowledge_base_id=?,notebook_id=?,title=?,content=?,content_text=?,updated_at=?
		WHERE id=? AND user_id=? AND deleted=0`, nullable(kbID), nullable(notebookID), in.Title, in.Content, in.ContentText, now, id, uid)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		jsonOut(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	jsonOut(w, http.StatusOK, noteJSON(id, kbID, notebookID, in.Title, in.Content, in.ContentText, "", now))
}

func (a *App) deleteNote(w http.ResponseWriter, uid int64, id string) {
	res, err := a.db.Exec("UPDATE notes SET deleted=1,updated_at=? WHERE id=? AND user_id=? AND deleted=0", time.Now().UTC().Format(time.RFC3339), id, uid)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		jsonOut(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func noteJSON(id, kbID, notebookID, title, content, contentText, created, updated string) map[string]any {
	return map[string]any{"id": id, "knowledgeBaseId": emptyToNil(kbID), "notebookId": emptyToNil(notebookID), "title": title, "content": content, "contentText": contentText, "createdAt": created, "updatedAt": updated}
}
func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
