package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// sessionsAPI owns the HTTP contract for conversation history.
func (a *App) sessionsAPI(w http.ResponseWriter, r *http.Request, uid int64, id string) {
	if r.Method == http.MethodGet {
		if id != "" {
			var title, mode, created, updated string
			if err := a.db.QueryRow("SELECT title,mode,created_at,updated_at FROM sessions WHERE id=? AND user_id=?", id, uid).Scan(&title, &mode, &created, &updated); err != nil {
				jsonOut(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			jsonOut(w, http.StatusOK, map[string]any{"id": id, "title": title, "mode": mode, "createdAt": created, "updatedAt": updated})
			return
		}
		rows, err := a.db.Query(`SELECT s.id,s.title,s.mode,s.created_at,s.updated_at,
 COALESCE((SELECT content FROM messages WHERE session_id=s.id AND role='user' ORDER BY id DESC LIMIT 1),'')
 FROM sessions s WHERE s.user_id=? ORDER BY s.updated_at DESC`, uid)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var sid, title, mode, created, updated, preview string
			if rows.Scan(&sid, &title, &mode, &created, &updated, &preview) == nil {
				out = append(out, map[string]any{"id": sid, "title": title, "mode": mode, "createdAt": created, "updatedAt": updated, "preview": preview})
			}
		}
		jsonOut(w, 200, out)
		return
	}
	if r.Method == http.MethodPost && id == "" {
		var in struct{ Title, Mode string }
		if !readJSON(r, &in) {
			jsonOut(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		if in.Mode == "" {
			in.Mode = "chat"
		}
		now := time.Now().UTC().Format(time.RFC3339)
		id = randomToken()
		if _, err := a.db.Exec("INSERT INTO sessions(id,user_id,title,mode,created_at,updated_at) VALUES(?,?,?,?,?,?)", id, uid, in.Title, in.Mode, now, now); err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		jsonOut(w, 201, map[string]any{"id": id, "title": in.Title, "mode": in.Mode, "createdAt": now, "updatedAt": now})
		return
	}
	if id != "" && r.Method == http.MethodPut {
		var in struct {
			Title string `json:"title"`
		}
		if !readJSON(r, &in) || in.Title == "" {
			jsonOut(w, 400, map[string]string{"error": "invalid title"})
			return
		}
		res, err := a.db.Exec("UPDATE sessions SET title=?,updated_at=? WHERE id=? AND user_id=?", in.Title, time.Now().UTC().Format(time.RFC3339), id, uid)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			jsonOut(w, 404, map[string]string{"error": "not found"})
			return
		}
		jsonOut(w, 200, map[string]any{"id": id, "title": in.Title})
		return
	}
	if id != "" && r.Method == http.MethodDelete {
		res, err := a.db.Exec("DELETE FROM sessions WHERE id=? AND user_id=?", id, uid)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			jsonOut(w, 404, map[string]string{"error": "not found"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (a *App) messagesAPI(w http.ResponseWriter, r *http.Request, uid int64, sessionID string) {
	if sessionID == "" || (r.Method != http.MethodGet && r.Method != http.MethodPost) {
		w.WriteHeader(405)
		return
	}
	var exists int
	if a.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE id=? AND user_id=?", sessionID, uid).Scan(&exists) != nil || exists == 0 {
		jsonOut(w, 404, map[string]string{"error": "session not found"})
		return
	}
	if r.Method == http.MethodPost {
		var in struct {
			Role, Content string
			Metadata      any
		}
		if !readJSON(r, &in) {
			jsonOut(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		meta, _ := json.Marshal(in.Metadata)
		now := time.Now().UTC().Format(time.RFC3339)
		res, err := a.db.Exec("INSERT INTO messages(session_id,user_id,role,content,metadata,created_at) VALUES(?,?,?,?,?,?)", sessionID, uid, in.Role, in.Content, string(meta), now)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		mid, _ := res.LastInsertId()
		a.db.Exec("UPDATE sessions SET updated_at=? WHERE id=?", now, sessionID)
		jsonOut(w, 201, map[string]any{"id": mid, "sessionId": sessionID, "role": in.Role, "content": in.Content, "metadata": in.Metadata, "createdAt": now})
		return
	}
	rows, err := a.db.Query("SELECT id,role,content,metadata,created_at FROM messages WHERE session_id=? AND user_id=? ORDER BY id", sessionID, uid)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var mid int64
		var role, content, meta, created string
		if rows.Scan(&mid, &role, &content, &meta, &created) == nil {
			var metadata any
			if meta != "" {
				_ = json.Unmarshal([]byte(meta), &metadata)
			}
			out = append(out, map[string]any{"id": mid, "sessionId": sessionID, "role": role, "content": content, "metadata": metadata, "createdAt": created})
		}
	}
	jsonOut(w, 200, out)
}
