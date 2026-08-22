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
CREATE TABLE IF NOT EXISTS notes (id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, knowledge_base_id TEXT, notebook_id TEXT, title TEXT NOT NULL DEFAULT '新建笔记', content TEXT NOT NULL DEFAULT '', content_text TEXT NOT NULL DEFAULT '', deleted INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS schedule_events (id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT NOT NULL DEFAULT '', start_at TEXT NOT NULL DEFAULT '', end_at TEXT NOT NULL DEFAULT '', start_time TEXT NOT NULL DEFAULT '', end_time TEXT NOT NULL DEFAULT '', all_day INTEGER NOT NULL DEFAULT 0, description TEXT NOT NULL DEFAULT '', color TEXT NOT NULL DEFAULT '#60a5fa', reminder INTEGER NOT NULL DEFAULT 0, completed INTEGER NOT NULL DEFAULT 0, priority TEXT NOT NULL DEFAULT 'important', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);`)
	if err != nil {
		return err
	}
	if _, err = a.db.Exec("CREATE INDEX IF NOT EXISTS idx_notes_user_updated ON notes(user_id, deleted, updated_at DESC)"); err != nil {
		return err
	}
	if _, err = a.db.Exec("CREATE INDEX IF NOT EXISTS idx_schedule_events_user_start ON schedule_events(user_id, start_at, start_time)"); err != nil {
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
	mux.HandleFunc("/api/knowledge/search", a.knowledgeSearch)
	mux.HandleFunc("/", a.adminPage)
	return logging(cors(mux))
}

// Vector search is intentionally a separate server boundary. The client keeps source
// documents locally, while the server stores/searches only the derived vector records.
// The Zvec worker will implement this endpoint without receiving source files.
func (a *App) knowledgeSearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.currentUser(r); !ok {
		jsonOut(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	jsonOut(w, http.StatusNotImplemented, map[string]string{"error": "server vector index is not initialized"})
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
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
