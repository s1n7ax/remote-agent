package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuth(t *testing.T) {
	a := &Auth{Token: "tok", Secret: []byte("s")}
	s := &Server{Auth: a}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := securityHeaders(a.middleware(inner))
	do := func(method, path string, mod func(*http.Request)) int {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("X-Requested-With", "remote-agent")
		if mod != nil {
			mod(r)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, c := range []struct {
		method, path string
		mod          func(*http.Request)
		want         int
	}{
		{"POST", "/api/projects/1/pulls/2/merge", nil, 401},
		{"GET", "/api/state", nil, 401},
		{"GET", "/api/artifacts/x", nil, 401},
		{"POST", "/api/drain", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, 401},
		{"POST", "/api/drain", func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }, 418},
		{"GET", "/api/state", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.session()}) }, 418},
		{"GET", "/api/state", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "forged"}) }, 401},
		{"GET", "/healthz", nil, 418},
		{"GET", "/api/idle", nil, 418},
		{"GET", "/", nil, 418},
		{"POST", "/api/login", nil, 418},
	} {
		if got := do(c.method, c.path, c.mod); got != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, got, c.want)
		}
	}

	login := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"token":"`+token+`"}`))
		w := httptest.NewRecorder()
		if err := s.login(w, r); err != nil {
			s.writeError(w, r, err)
		}
		return w
	}
	if w := login("wrong"); w.Code != 401 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("wrong token: %d %v", w.Code, w.Result().Cookies())
	}
	w := login("tok")
	cs := w.Result().Cookies()
	if w.Code != 200 || len(cs) != 1 || cs[0].Value != a.session() || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("login: %d %+v", w.Code, cs)
	}
}
