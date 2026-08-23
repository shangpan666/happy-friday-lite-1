package main

import (
	"encoding/json"
	"fmt"
	zvec "github.com/zvec-ai/zvec-go"
	"net/http"
	"os"
	"strings"
)

type vectorChunk struct {
	ID       string         `json:"id"`
	Source   string         `json:"source"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata"`
}
type indexRequest struct {
	KBType string        `json:"kbType"`
	Source string        `json:"source"`
	Chunks []vectorChunk `json:"chunks"`
}

func (a *App) knowledgeAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := a.currentUser(r)
	if !ok {
		jsonOut(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	switch r.URL.Path {
	case "/api/knowledge/index":
		if r.Method == http.MethodPost {
			a.indexKnowledge(w, r, u.ID)
			return
		}
	case "/api/knowledge/search":
		if r.Method == http.MethodPost {
			a.searchKnowledge(w, r, u.ID)
			return
		}
	case "/api/knowledge/summary":
		if r.Method == http.MethodGet {
			a.knowledgeSummary(w, r, u.ID)
			return
		}
	case "/api/knowledge/clear":
		if r.Method == http.MethodPost {
			a.clearKnowledge(w, r, u.ID)
			return
		}
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}
func (a *App) knowledgeSummary(w http.ResponseWriter, r *http.Request, uid int64) {
	jsonOut(w, 200, map[string]any{"summary": map[string]any{}})
}
func (a *App) clearKnowledge(w http.ResponseWriter, r *http.Request, uid int64) {
	var in struct {
		KBType string `json:"kbType"`
	}
	_ = readJSON(r, &in)
	dataDir := os.Getenv("HAPPY_FRIDAY_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	db, err := openVectorDB(dataDir)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": err.Error()})
		return
	}
	filter := fmt.Sprintf("user_id = '%d'", uid)
	if in.KBType != "" {
		filter += fmt.Sprintf(" AND kb_type = '%s'", filterValue(in.KBType))
	}
	err = db.DeleteByFilter(filter)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, 200, map[string]any{"success": true})
}

func (a *App) indexKnowledge(w http.ResponseWriter, r *http.Request, uid int64) {
	var in indexRequest
	if !readJSON(r, &in) || in.KBType == "" || in.Source == "" {
		jsonOut(w, 400, map[string]string{"error": "kbType and source are required"})
		return
	}
	dataDir := os.Getenv("HAPPY_FRIDAY_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	db, err := openVectorDB(dataDir)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": err.Error()})
		return
	}
	n, err := vectorInsert(db, uid, in)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, 200, map[string]any{"success": true, "count": n})
}

func readableChunk(content string) bool {
	if strings.TrimSpace(content) == "" || strings.Contains(content, "%PDF-") || strings.Contains(content, "endobj") || strings.Contains(content, "FlateDecode") {
		return false
	}
	control := 0
	for _, r := range content {
		if (r < 32 && r != '\n' && r != '\r' && r != '\t') || r == '\ufffd' {
			control++
		}
	}
	return control <= len([]rune(content))/20
}
func (a *App) searchKnowledge(w http.ResponseWriter, r *http.Request, uid int64) {
	var in struct {
		Query          string  `json:"query"`
		KBType         string  `json:"kbType"`
		TopK           int     `json:"topK"`
		ScoreThreshold float64 `json:"scoreThreshold"`
	}
	if !readJSON(r, &in) || strings.TrimSpace(in.Query) == "" {
		jsonOut(w, 400, map[string]string{"error": "query required"})
		return
	}
	if in.TopK <= 0 {
		in.TopK = 10
	}
	dataDir := os.Getenv("HAPPY_FRIDAY_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	db, err := openVectorDB(dataDir)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": err.Error()})
		return
	}
	q := zvec.NewSearchQuery()
	defer q.Destroy()
	q.SetFieldName("embedding")
	v := embedText(in.Query)
	fv := make([]float32, len(v))
	for i, x := range v {
		fv[i] = float32(x)
	}
	q.SetQueryVector(fv)
	q.SetTopK(in.TopK)
	filter := fmt.Sprintf("user_id = '%d'", uid)
	if in.KBType != "" {
		filter += fmt.Sprintf(" AND kb_type = '%s'", filterValue(in.KBType))
	}
	q.SetFilter(filter)
	docs, err := db.Query(q)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer zvec.FreeDocs(docs)
	out := []map[string]any{}
	for _, d := range docs {
		s, _ := d.GetStringField("source")
		c, _ := d.GetStringField("content")
		m, _ := d.GetStringField("metadata")
		var md map[string]any
		_ = json.Unmarshal([]byte(m), &md)
		if readableChunk(c) && float64(d.GetScore()) >= in.ScoreThreshold {
			out = append(out, map[string]any{"source": s, "content": c, "metadata": md, "score": d.GetScore()})
		}
	}
	jsonOut(w, 200, map[string]any{"results": out})
}
