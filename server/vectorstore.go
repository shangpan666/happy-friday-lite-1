package main

import (
	"encoding/json"
	"fmt"
	zvec "github.com/zvec-ai/zvec-go"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const vectorDimension = 64

var vectorState struct {
	sync.Mutex
	db *zvec.Collection
}

func openVectorDB(dataDir string) (*zvec.Collection, error) {
	vectorState.Lock()
	defer vectorState.Unlock()
	if vectorState.db != nil {
		return vectorState.db, nil
	}
	if err := zvec.Initialize(nil); err != nil && !zvec.IsInitialized() {
		return nil, err
	}
	root := filepath.Join(dataDir, "rag", "zvec")
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(root); err == nil {
		db, e := zvec.Open(root, nil)
		if e == nil {
			vectorState.db = db
			return db, nil
		}
	}
	schema := zvec.NewCollectionSchema("knowledge")
	invert, e := zvec.NewInvertIndexParams(true, false)
	if e != nil {
		return nil, e
	}
	defer invert.Destroy()
	hnsw, e := zvec.NewHNSWIndexParams(zvec.MetricTypeCosine, 16, 200)
	if e != nil {
		return nil, e
	}
	defer hnsw.Destroy()
	for _, name := range []string{"user_id", "kb_type", "source", "content", "metadata"} {
		f := zvec.NewFieldSchema(name, zvec.DataTypeString, true, 0)
		if name != "content" {
			_ = f.SetIndexParams(invert)
		}
		if e := schema.AddField(f); e != nil {
			return nil, e
		}
	}
	vf := zvec.NewFieldSchema("embedding", zvec.DataTypeVectorFP32, false, vectorDimension)
	if e := vf.SetIndexParams(hnsw); e != nil {
		return nil, e
	}
	if e := schema.AddField(vf); e != nil {
		return nil, e
	}
	db, e := zvec.CreateAndOpen(root, schema, nil)
	if e != nil {
		return nil, e
	}
	vectorState.db = db
	return db, nil
}
func filterValue(s string) string { return strings.ReplaceAll(s, "'", "''") }
func vectorDelete(db *zvec.Collection, uid int64, kb, source string) error {
	return db.DeleteByFilter(fmt.Sprintf("user_id = '%d' AND kb_type = '%s' AND source = '%s'", uid, filterValue(kb), filterValue(source)))
}
func vectorInsert(db *zvec.Collection, uid int64, in indexRequest) (int, error) {
	if e := vectorDelete(db, uid, in.KBType, in.Source); e != nil {
		return 0, e
	}
	docs := make([]*zvec.Doc, 0, len(in.Chunks))
	for _, c := range in.Chunks {
		if !readableChunk(c.Content) {
			continue
		}
		// Zvec primary keys only accept its restricted identifier charset. The
		// source path is metadata, never a primary key, because it contains '/' ':'
		// and other characters rejected by Zvec.
		id := randomToken()
		d := zvec.NewDoc()
		d.SetPK(id)
		_ = d.AddStringField("user_id", fmt.Sprint(uid))
		_ = d.AddStringField("kb_type", in.KBType)
		_ = d.AddStringField("source", in.Source)
		_ = d.AddStringField("content", c.Content)
		m, _ := json.Marshal(c.Metadata)
		_ = d.AddStringField("metadata", string(m))
		e := embedText(c.Content)
		v := make([]float32, len(e))
		for i, x := range e {
			v[i] = float32(x)
		}
		_ = d.AddVectorFP32Field("embedding", v)
		docs = append(docs, d)
	}
	if len(docs) == 0 {
		return 0, nil
	}
	r, e := db.Insert(docs)
	for _, d := range docs {
		d.Destroy()
	}
	if e != nil {
		return 0, e
	}
	if e = db.Flush(); e != nil {
		return 0, e
	}
	return int(r.SuccessCount), nil
}
