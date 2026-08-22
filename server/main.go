package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type App struct {
	db       *sql.DB
	tokenTTL time.Duration
}
type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type user struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	CreatedAt   string `json:"createdAt"`
}

func main() {
	dataDir := os.Getenv("HAPPY_FRIDAY_DATA_DIR")
	if dataDir == "" {
		dataDir = filepath.Join("data")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "friday.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	app := &App{db: db, tokenTTL: 24 * time.Hour}
	if err := app.migrate(); err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("HAPPY_FRIDAY_ADDR")
	if addr == "" {
		addr = ":17918"
	}
	log.Printf("Happy Friday server listening on http://%s", displayAddr(addr))
	log.Printf("Admin account is created from HAPPY_FRIDAY_ADMIN_USER/PASSWORD on first start")
	log.Fatal(http.ListenAndServe(addr, app.routes()))
}

func (a *App) migrate() error {
	_, err := a.db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, password_salt TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT 'employee', disabled INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS tokens (token TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires_at TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS audit_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, action TEXT NOT NULL, metadata TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL);`)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT NOT NULL DEFAULT '新对话', mode TEXT NOT NULL DEFAULT 'chat', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, role TEXT NOT NULL, content TEXT NOT NULL, metadata TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS notes (id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT NOT NULL DEFAULT '新建笔记', content TEXT NOT NULL DEFAULT '', content_text TEXT NOT NULL DEFAULT '', deleted INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS schedule_events (id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT NOT NULL DEFAULT '', start_at TEXT NOT NULL DEFAULT '', end_at TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', completed INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);`)
	if err != nil {
		return err
	}
	var count int
	if err = a.db.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		name, pass := os.Getenv("HAPPY_FRIDAY_ADMIN_USER"), os.Getenv("HAPPY_FRIDAY_ADMIN_PASSWORD")
		if name == "" {
			name = "admin"
		}
		if pass == "" {
			pass = "change-me-now"
		}
		_, err = a.createUser(name, pass, "管理员", "admin")
	}
	return err
}

func (a *App) createUser(name, password, display, role string) (user, error) {
	salt, hash := hashPassword(password)
	now := time.Now().UTC().Format(time.RFC3339)
	r, err := a.db.Exec("INSERT INTO users(username,password_hash,password_salt,display_name,role,created_at) VALUES(?,?,?,?,?,?)", name, hash, salt, display, role, now)
	if err != nil {
		return user{}, err
	}
	id, _ := r.LastInsertId()
	return user{ID: id, Username: name, DisplayName: display, Role: role, CreatedAt: now}, nil
}

func hashPassword(password string) (string, string) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	salt := hex.EncodeToString(b)
	sum := []byte(salt + password)
	for i := 0; i < 120000; i++ {
		h := sha256.Sum256(sum)
		sum = h[:]
	}
	return salt, hex.EncodeToString(sum)
}
func checkPassword(password, salt, expected string) bool {
	sum := []byte(salt + password)
	for i := 0; i < 120000; i++ {
		h := sha256.Sum256(sum)
		sum = h[:]
	}
	return hex.EncodeToString(sum) == expected
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.health)
	mux.HandleFunc("/api/auth/login", a.login)
	mux.HandleFunc("/api/auth/me", a.authMe)
	mux.HandleFunc("/api/admin/users", a.adminUsers)
	mux.HandleFunc("/api/admin/data", a.adminData)
	mux.HandleFunc("/api/data/", a.employeeData)
	mux.HandleFunc("/", a.adminPage)
	return logging(cors(mux))
}
func (a *App) health(w http.ResponseWriter, _ *http.Request) {
	jsonOut(w, 200, map[string]any{"ok": true, "service": "happy-friday", "time": time.Now().UTC()})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(r, &in) || in.Username == "" || in.Password == "" {
		jsonOut(w, 400, map[string]string{"error": "invalid credentials"})
		return
	}
	var id int64
	var hash, salt, role string
	var disabled int
	err := a.db.QueryRow("SELECT id,password_hash,password_salt,role,disabled FROM users WHERE username=?", in.Username).Scan(&id, &hash, &salt, &role, &disabled)
	if err != nil || disabled != 0 || !checkPassword(in.Password, salt, hash) {
		jsonOut(w, 401, map[string]string{"error": "用户名或密码错误"})
		return
	}
	token := randomToken()
	exp := time.Now().UTC().Add(a.tokenTTL)
	_, err = a.db.Exec("INSERT INTO tokens(token,user_id,expires_at,created_at) VALUES(?,?,?,?)", token, id, exp.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": "login failed"})
		return
	}
	jsonOut(w, 200, map[string]any{"accessToken": token, "expiresAt": exp, "user": user{ID: id, Username: in.Username, Role: role}})
}
func (a *App) authMe(w http.ResponseWriter, r *http.Request) {
	u, ok := a.currentUser(r)
	if !ok {
		jsonOut(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	jsonOut(w, 200, u)
}
func (a *App) adminUsers(w http.ResponseWriter, r *http.Request) {
	u, ok := a.currentUser(r)
	if !ok || u.Role != "admin" {
		jsonOut(w, 403, map[string]string{"error": "管理员权限 required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.Query("SELECT id,username,display_name,role,disabled,created_at FROM users ORDER BY id")
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := []user{}
		for rows.Next() {
			var x user
			var disabled int
			if rows.Scan(&x.ID, &x.Username, &x.DisplayName, &x.Role, &disabled, &x.CreatedAt) == nil {
				x.Disabled = disabled != 0
				out = append(out, x)
			}
		}
		jsonOut(w, 200, out)
	case http.MethodPost:
		var in struct {
			credentials
			DisplayName string `json:"displayName"`
		}
		if !readJSON(r, &in) || in.Username == "" || len(in.Password) < 8 {
			jsonOut(w, 400, map[string]string{"error": "用户名不能为空，密码至少 8 位"})
			return
		}
		x, err := a.createUser(in.Username, in.Password, in.DisplayName, "employee")
		if err != nil {
			jsonOut(w, 409, map[string]string{"error": "用户名已存在"})
			return
		}
		jsonOut(w, 201, x)
	default:
		w.WriteHeader(405)
	}
}
func (a *App) adminData(w http.ResponseWriter, r *http.Request) {
	u, ok := a.currentUser(r)
	if !ok || u.Role != "admin" {
		jsonOut(w, 403, map[string]string{"error": "管理员权限 required"})
		return
	}
	tables := map[string]string{"sessions": "SELECT s.id,s.user_id,u.username,s.title,s.mode,s.created_at,s.updated_at FROM sessions s JOIN users u ON u.id=s.user_id ORDER BY s.updated_at DESC LIMIT 500", "notes": "SELECT n.id,n.user_id,u.username,n.title,n.created_at,n.updated_at FROM notes n JOIN users u ON u.id=n.user_id WHERE n.deleted=0 ORDER BY n.updated_at DESC LIMIT 500", "schedule_events": "SELECT e.id,e.user_id,u.username,e.title,e.start_at,e.end_at,e.updated_at FROM schedule_events e JOIN users u ON u.id=e.user_id ORDER BY e.updated_at DESC LIMIT 500"}
	out := map[string]any{}
	for name, q := range tables {
		rows, err := a.db.Query(q)
		if err != nil {
			continue
		}
		cols, _ := rows.Columns()
		vals := []map[string]any{}
		for rows.Next() {
			raw := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range raw {
				ptr[i] = &raw[i]
			}
			if rows.Scan(ptr...) == nil {
				m := map[string]any{}
				for i, c := range cols {
					m[c] = raw[i]
				}
				vals = append(vals, m)
			}
		}
		rows.Close()
		out[name] = vals
	}
	jsonOut(w, 200, out)
}

func (a *App) employeeData(w http.ResponseWriter, r *http.Request) {
	u, ok := a.currentUser(r)
	if !ok {
		jsonOut(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/data/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		jsonOut(w, 404, map[string]string{"error": "resource not found"})
		return
	}
	resource, id := parts[0], ""
	if len(parts) > 1 {
		id = parts[1]
	}
	switch resource {
	case "notes":
		a.notesAPI(w, r, u.ID, id)
	case "sessions":
		a.sessionsAPI(w, r, u.ID, id)
	case "messages":
		a.messagesAPI(w, r, u.ID, id)
	case "schedule-events":
		a.scheduleAPI(w, r, u.ID, id)
	default:
		jsonOut(w, 404, map[string]string{"error": "resource not found"})
	}
}

func (a *App) notesAPI(w http.ResponseWriter, r *http.Request, uid int64, id string) {
	if r.Method == http.MethodGet && id == "" {
		rows, err := a.db.Query("SELECT id,title,content,content_text,created_at,updated_at FROM notes WHERE user_id=? AND deleted=0 ORDER BY updated_at DESC", uid)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var x map[string]any
			var noteID, title, content, text, created, updated string
			if rows.Scan(&noteID, &title, &content, &text, &created, &updated) == nil {
				x = map[string]any{"id": noteID, "title": title, "content": content, "contentText": text, "createdAt": created, "updatedAt": updated}
				out = append(out, x)
			}
		}
		jsonOut(w, 200, out)
		return
	}
	if r.Method == http.MethodPost && id == "" {
		var in struct{ Title, Content, ContentText string }
		if !readJSON(r, &in) {
			jsonOut(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		id = randomToken()
		_, err := a.db.Exec("INSERT INTO notes(id,user_id,title,content,content_text,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", id, uid, in.Title, in.Content, in.ContentText, now, now)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		jsonOut(w, 201, map[string]any{"id": id, "title": in.Title, "content": in.Content, "contentText": in.ContentText, "createdAt": now, "updatedAt": now})
		return
	}
	if (r.Method == http.MethodPut || r.Method == http.MethodDelete) && id != "" {
		if r.Method == http.MethodDelete {
			res, err := a.db.Exec("UPDATE notes SET deleted=1,updated_at=? WHERE id=? AND user_id=?", time.Now().UTC().Format(time.RFC3339), id, uid)
			if err != nil {
				jsonOut(w, 500, map[string]string{"error": err.Error()})
				return
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				jsonOut(w, 404, map[string]string{"error": "not found"})
				return
			}
			w.WriteHeader(204)
			return
		}
		var in struct{ Title, Content, ContentText string }
		if !readJSON(r, &in) {
			jsonOut(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		res, err := a.db.Exec("UPDATE notes SET title=?,content=?,content_text=?,updated_at=? WHERE id=? AND user_id=? AND deleted=0", in.Title, in.Content, in.ContentText, time.Now().UTC().Format(time.RFC3339), id, uid)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			jsonOut(w, 404, map[string]string{"error": "not found"})
			return
		}
		jsonOut(w, 200, map[string]any{"id": id, "title": in.Title, "content": in.Content, "contentText": in.ContentText})
		return
	}
	if (r.Method == http.MethodPut || r.Method == http.MethodDelete) && id != "" {
		if r.Method == http.MethodDelete {
			res, err := a.db.Exec("DELETE FROM schedule_events WHERE id=? AND user_id=?", id, uid)
			if err != nil {
				jsonOut(w, 500, map[string]string{"error": err.Error()})
				return
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				jsonOut(w, 404, map[string]string{"error": "not found"})
				return
			}
			w.WriteHeader(204)
			return
		}
		var in struct {
			Title, Start, End, Description string
			Completed                      bool
		}
		if !readJSON(r, &in) {
			jsonOut(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		res, err := a.db.Exec("UPDATE schedule_events SET title=?,start_at=?,end_at=?,description=?,completed=?,updated_at=? WHERE id=? AND user_id=?", in.Title, in.Start, in.End, in.Description, boolInt(in.Completed), time.Now().UTC().Format(time.RFC3339), id, uid)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			jsonOut(w, 404, map[string]string{"error": "not found"})
			return
		}
		jsonOut(w, 200, map[string]any{"id": id, "title": in.Title, "start": in.Start, "end": in.End, "description": in.Description, "completed": in.Completed})
		return
	}
	w.WriteHeader(405)
}

func (a *App) sessionsAPI(w http.ResponseWriter, r *http.Request, uid int64, id string) {
	if r.Method == http.MethodGet {
		if id != "" {
			var title, mode, created, updated string
			err := a.db.QueryRow("SELECT title,mode,created_at,updated_at FROM sessions WHERE id=? AND user_id=?", id, uid).Scan(&title, &mode, &created, &updated)
			if err != nil {
				jsonOut(w, 404, map[string]string{"error": "not found"})
				return
			}
			jsonOut(w, 200, map[string]any{"id": id, "title": title, "mode": mode, "createdAt": created, "updatedAt": updated})
			return
		}
		rows, err := a.db.Query("SELECT id,title,mode,created_at,updated_at FROM sessions WHERE user_id=? ORDER BY updated_at DESC", uid)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var sid, title, mode, created, updated string
			if rows.Scan(&sid, &title, &mode, &created, &updated) == nil {
				out = append(out, map[string]any{"id": sid, "title": title, "mode": mode, "createdAt": created, "updatedAt": updated})
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
		_, err := a.db.Exec("INSERT INTO sessions(id,user_id,title,mode,created_at,updated_at) VALUES(?,?,?,?,?,?)", id, uid, in.Title, in.Mode, now, now)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		jsonOut(w, 201, map[string]any{"id": id, "title": in.Title, "mode": in.Mode, "createdAt": now, "updatedAt": now})
		return
	}
	w.WriteHeader(405)
}

func (a *App) messagesAPI(w http.ResponseWriter, r *http.Request, uid int64, sessionID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost || sessionID == "" {
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
		now := time.Now().UTC().Format(time.RFC3339)
		meta, _ := json.Marshal(in.Metadata)
		res, err := a.db.Exec("INSERT INTO messages(session_id,user_id,role,content,metadata,created_at) VALUES(?,?,?,?,?,?)", sessionID, uid, in.Role, in.Content, string(meta), now)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		mid, _ := res.LastInsertId()
		jsonOut(w, 201, map[string]any{"id": mid, "sessionId": sessionID, "role": in.Role, "content": in.Content, "createdAt": now})
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
			out = append(out, map[string]any{"id": mid, "role": role, "content": content, "metadata": meta, "createdAt": created})
		}
	}
	jsonOut(w, 200, out)
}

func (a *App) scheduleAPI(w http.ResponseWriter, r *http.Request, uid int64, id string) {
	if r.Method == http.MethodGet {
		q := "SELECT id,title,start_at,end_at,description,completed,created_at,updated_at FROM schedule_events WHERE user_id=?"
		args := []any{uid}
		if id != "" {
			q += " AND id=?"
			args = append(args, id)
		}
		rows, err := a.db.Query(q, args...)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var eid, title, start, end, desc, created, updated string
			var completed int
			if rows.Scan(&eid, &title, &start, &end, &desc, &completed, &created, &updated) == nil {
				out = append(out, map[string]any{"id": eid, "title": title, "start": start, "end": end, "description": desc, "completed": completed != 0, "createdAt": created, "updatedAt": updated})
			}
		}
		if id != "" {
			if len(out) == 0 {
				jsonOut(w, 404, map[string]string{"error": "not found"})
			} else {
				jsonOut(w, 200, out[0])
			}
		} else {
			jsonOut(w, 200, out)
		}
		return
	}
	if r.Method == http.MethodPost && id == "" {
		var in struct {
			Title, Start, End, Description string
			Completed                      bool
		}
		if !readJSON(r, &in) {
			jsonOut(w, 400, map[string]string{"error": "invalid body"})
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		id = randomToken()
		_, err := a.db.Exec("INSERT INTO schedule_events(id,user_id,title,start_at,end_at,description,completed,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", id, uid, in.Title, in.Start, in.End, in.Description, boolInt(in.Completed), now, now)
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		jsonOut(w, 201, map[string]any{"id": id, "title": in.Title, "start": in.Start, "end": in.End, "description": in.Description, "completed": in.Completed, "createdAt": now, "updatedAt": now})
		return
	}
	w.WriteHeader(405)
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func (a *App) adminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Happy Friday 管理后台</title><style>body{font:14px system-ui;max-width:1100px;margin:32px auto;padding:0 18px;color:#222}input,button{padding:9px;margin:4px;border:1px solid #ccd2d8;border-radius:4px}button{cursor:pointer;background:#1677ff;color:#fff;border:0}section{border-top:1px solid #e5e7eb;margin-top:24px;padding-top:16px}table{border-collapse:collapse;width:100%;margin-top:10px}td,th{padding:8px;border-bottom:1px solid #eee;text-align:left}pre{background:#f6f8fa;padding:12px;overflow:auto;max-height:420px}.muted{color:#667085}.hidden{display:none}</style><h1>Happy Friday 企业管理后台</h1><div id="login"><p class="muted">使用管理员账号登录。</p><input id="u" placeholder="用户名" value="admin"><input id="p" type="password" placeholder="密码"><button onclick="login()">登录</button><span id="err"></span></div><div id="app" class="hidden"><p>已登录。<button onclick="loadAll()">刷新数据</button><button onclick="logout()">退出</button></p><section><h2>员工账号</h2><input id="newu" placeholder="员工用户名"><input id="newp" type="password" placeholder="初始密码（至少 8 位）"><input id="newd" placeholder="显示名称"><button onclick="createUser()">创建员工</button><div id="users"></div></section><section><h2>业务数据</h2><pre id="data">加载中...</pre></section></div><script>let token='';const $=id=>document.getElementById(id);async function login(){const r=await fetch('/api/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:$('u').value,password:$('p').value})});const x=await r.json();if(!r.ok){$('err').textContent=x.error||'登录失败';return}token=x.accessToken;$('login').className='hidden';$('app').className='';loadAll()}async function api(path,opt={}){opt.headers=Object.assign({'Authorization':'Bearer '+token},opt.headers||{});const r=await fetch(path,opt);return r.json()}async function loadAll(){const us=await api('/api/admin/users');$('users').innerHTML='<table><tr><th>ID</th><th>用户名</th><th>名称</th><th>角色</th><th>状态</th></tr>'+us.map(x=>'<tr><td>'+x.id+'</td><td>'+x.username+'</td><td>'+x.displayName+'</td><td>'+x.role+'</td><td>'+(x.disabled?'已禁用':'正常')+'</td></tr>').join('')+'</table>';const d=await api('/api/admin/data');$('data').textContent=JSON.stringify(d,null,2)}async function createUser(){const r=await api('/api/admin/users',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:$('newu').value,password:$('newp').value,displayName:$('newd').value})});if(r.error){alert(r.error);return}$('newu').value='';$('newp').value='';$('newd').value='';loadAll()}function logout(){token='';$('app').className='hidden';$('login').className=''}</script></html>`)
}

func (a *App) currentUser(r *http.Request) (user, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return user{}, false
	}
	var u user
	var disabled int
	var exp string
	err := a.db.QueryRow("SELECT u.id,u.username,u.display_name,u.role,u.disabled,u.created_at,t.expires_at FROM tokens t JOIN users u ON u.id=t.user_id WHERE t.token=?", strings.TrimPrefix(h, "Bearer ")).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &disabled, &u.CreatedAt, &exp)
	if err != nil || disabled != 0 {
		return user{}, false
	}
	if t, _ := time.Parse(time.RFC3339, exp); time.Now().UTC().After(t) {
		return user{}, false
	}
	u.Disabled = disabled != 0
	return u, true
}
func randomToken() string                  { b := make([]byte, 32); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func readJSON(r *http.Request, v any) bool { return json.NewDecoder(r.Body).Decode(v) == nil }
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}
func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		if ip := localIP(); ip != "" {
			return net.JoinHostPort(ip, strings.TrimPrefix(addr, ":"))
		}
	}
	return addr
}
func localIP() string {
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			ip, _, _ := net.ParseCIDR(a.String())
			if ip != nil && ip.To4() != nil {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}
