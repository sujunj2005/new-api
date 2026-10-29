package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ---- sessionLifetimeExpired 纯逻辑单测 ----

type fakeSession struct {
	values map[string]interface{}
}

func (f *fakeSession) Get(key interface{}) interface{} { return f.values[key.(string)] }
func (f *fakeSession) Set(key interface{}, val interface{}) {
	f.values[key.(string)] = val
}
func (f *fakeSession) Delete(key interface{})                     { delete(f.values, key.(string)) }
func (f *fakeSession) Clear()                                     { f.values = map[string]interface{}{} }
func (f *fakeSession) ID() string                                 { return "" }
func (f *fakeSession) AddFlash(value interface{}, vars ...string) {}
func (f *fakeSession) Flashes(vars ...string) []interface{}       { return nil }
func (f *fakeSession) Options(options sessions.Options)           {}
func (f *fakeSession) Save() error                                { return nil }

func TestSessionLifetimeExpired(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name       string
		loginAt    int64
		lastActive int64
		wantReason string
		wantExpire bool
	}{
		{"missing timestamps", 0, 0, "missing_timestamp", true},
		{"idle 29:59 keeps session", now - 3600, now - 1799, "", false},
		{"idle 30:00 expires", now - 12*3600, now - 1800, "idle_timeout", true},
		{"absolute 11:59:59 keeps session", now - 43199, now, "", false},
		{"absolute 12:00 expires", now - 43200, now, "absolute_timeout", true},
		{"future last_active expires", now - 60, now + 100, "future_last_active", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSession{values: map[string]interface{}{
				common.SessionKeyLoginAt:    tc.loginAt,
				common.SessionKeyLastActive: tc.lastActive,
			}}
			reason, expired := sessionLifetimeExpired(s, now)
			if expired != tc.wantExpire || reason != tc.wantReason {
				t.Fatalf("got (reason=%q, expired=%v), want (reason=%q, expired=%v)",
					reason, expired, tc.wantReason, tc.wantExpire)
			}
		})
	}
}

// ---- authenticateUser 会话生命周期集成测试（真实 cookie store 往返）----

const testSessionSecret = "unit-test-session-secret"

func newSessionTestRouter(loginAt, lastActive int64, writeTimestamps bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	store := cookie.NewStore([]byte(testSessionSecret))
	store.Options(sessions.Options{Path: "/", MaxAge: 0, HttpOnly: true})
	r := gin.New()
	r.Use(sessions.Sessions("session", store))
	r.GET("/login", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", 1)
		s.Set("username", "tester")
		s.Set("role", common.RoleCommonUser)
		s.Set("status", common.UserStatusEnabled)
		s.Set("group", "default")
		if writeTimestamps {
			s.Set(common.SessionKeyLoginAt, loginAt)
			s.Set(common.SessionKeyLastActive, lastActive)
		}
		_ = s.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/protected", UserAuth())
	return r
}

func requestProtectedWithLoginCookie(t *testing.T, r *gin.Engine, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	loginW := httptest.NewRecorder()
	r.ServeHTTP(loginW, httptest.NewRequest(http.MethodGet, "/login", nil))
	if loginW.Code != http.StatusOK {
		t.Fatalf("login failed: %d", loginW.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, ck := range loginW.Result().Cookies() {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSessionLifecycleEnforcement(t *testing.T) {
	now := time.Now().Unix()

	t.Run("fresh session passes", func(t *testing.T) {
		r := newSessionTestRouter(now, now, true)
		w := requestProtectedWithLoginCookie(t, r, map[string]string{"New-Api-User": "1"})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("idle timeout returns 401", func(t *testing.T) {
		r := newSessionTestRouter(now-3600, now-1800, true)
		w := requestProtectedWithLoginCookie(t, r, map[string]string{"New-Api-User": "1"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("absolute timeout returns 401 despite activity", func(t *testing.T) {
		r := newSessionTestRouter(now-43200, now, true)
		w := requestProtectedWithLoginCookie(t, r, map[string]string{"New-Api-User": "1"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("legacy session without timestamps expires once", func(t *testing.T) {
		// 模拟部署前签发的旧 Cookie：有登录态但无时间戳，首个受保护请求即 401
		r := newSessionTestRouter(0, 0, false)
		w := requestProtectedWithLoginCookie(t, r, map[string]string{"New-Api-User": "1"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
		}
	})
}

// ---- 纯 Authorization 访问令牌请求不做会话校验 ----

func TestPureAccessTokenSkipsSessionCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("migrate user: %v", err)
	}
	origDB := model.DB
	model.DB = db
	defer func() { model.DB = origDB }()

	token := "0123456789abcdef0123456789abcdef"
	user := model.User{
		Username:    "tokenuser",
		Password:    "password123",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AccessToken: &token,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	store := cookie.NewStore([]byte(testSessionSecret))
	r := gin.New()
	r.Use(sessions.Sessions("session", store))
	r.GET("/protected", UserAuth())

	// 无会话 Cookie 的纯 Authorization 请求：若会话校验未被 useAccessToken
	// 守卫跳过，将因时间戳缺失被判过期；期望访问令牌直接通过。
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", token)
	req.Header.Set("New-Api-User", "1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}
