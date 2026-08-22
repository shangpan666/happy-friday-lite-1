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
	tables := map[string]string{"sessions": "SELECT id,title,mode,createdAt,updatedAt FROM sessions ORDER BY updatedAt DESC LIMIT 500", "notes": "SELECT id,title,createdAt,updatedAt FROM notes WHERE isDeleted=0 ORDER BY updatedAt DESC LIMIT 500", "schedule_events": "SELECT id,title,start,end,updatedAt FROM schedule_events ORDER BY updatedAt DESC LIMIT 500"}
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
func (a *App) adminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Happy Friday 管理后台</title><style>body{font:14px system-ui;max-width:1000px;margin:40px auto;color:#222}input,button{padding:8px;margin:4px}pre{background:#f5f5f5;padding:16px;overflow:auto}h1{font-size:24px}</style><h1>Happy Friday 企业管理后台</h1><p>请使用管理员账号登录 API：<code>POST /api/auth/login</code>。管理页面将在后续版本提供完整交互。</p><p>健康检查：<a href="/health">/health</a></p></html>`)
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
