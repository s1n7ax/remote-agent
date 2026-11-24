// Package api serves remote-agent's HTTP JSON API, the live event stream and
// the embedded SPA.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/orchestrator"
	"github.com/s1n7ax/remote-agent/internal/skills"
	"github.com/s1n7ax/remote-agent/internal/store"
)

// Push is the push-notification side the API needs.
type Push interface {
	PublicKey() string
	Subscribe(ctx context.Context, subscription json.RawMessage) error
	Prefs(ctx context.Context) (map[string]bool, error)
	SetPrefs(ctx context.Context, prefs map[string]bool) error
	Test(ctx context.Context) error
}

// Server holds the API's dependencies.
type Server struct {
	Orch    *orchestrator.Orchestrator
	Store   *store.Store
	GitHub  orchestrator.GitHub
	Skills  *skills.Manager
	Bench   *skills.Bench
	Push    Push
	SPA     fs.FS // the built frontend (index.html at its root)
	Log     *slog.Logger
	Version string
	// OpenRouterConfigured reports whether an OpenRouter key is set.
	OpenRouterConfigured func() bool
	// MaxParallel reads/writes the parallel-run limit.
	MaxParallel func() int
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	mux := http.NewServeMux()
	h := func(pattern string, f func(w http.ResponseWriter, r *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := f(w, r); err != nil {
				s.writeError(w, r, err)
			}
		})
	}

	mux.HandleFunc("GET /healthz", s.health)
	h("GET /api/idle", s.idle)
	h("POST /api/drain", s.drain)
	h("GET /api/state", s.state)
	mux.HandleFunc("GET /api/events", s.events)

	h("GET /api/projects", s.listProjects)
	h("POST /api/projects", s.createProject)
	h("PATCH /api/projects/{id}", s.patchProject)
	h("DELETE /api/projects/{id}", s.deleteProject)
	h("GET /api/projects/{id}/board", s.board)
	h("POST /api/projects/{id}/tickets", s.createTicket)
	h("GET /api/projects/{id}/github", s.projectGitHub)
	h("POST /api/projects/{id}/pulls/{n}/merge", s.merge)
	h("GET /api/projects/{id}/schedules", s.listSchedules)
	h("POST /api/projects/{id}/schedules", s.createSchedule)
	h("PATCH /api/schedules/{sid}", s.patchSchedule)
	h("DELETE /api/schedules/{sid}", s.deleteSchedule)

	h("GET /api/tickets/{id}", s.ticket)
	h("PATCH /api/tickets/{id}", s.patchTicket)
	h("DELETE /api/tickets/{id}", s.deleteTicket)
	h("POST /api/tickets/{id}/start", s.ticketAction("start"))
	h("POST /api/tickets/{id}/retry", s.ticketAction("retry"))
	h("POST /api/tickets/{id}/pause", s.ticketAction("pause"))
	h("POST /api/tickets/{id}/resume", s.ticketAction("resume"))
	h("POST /api/tickets/{id}/stop", s.ticketAction("stop"))
	h("POST /api/tickets/{id}/message", s.message)
	h("PUT /api/tickets/{id}/rounds/{n}/answers/{qid}", s.answer)
	h("GET /api/tickets/{id}/runs", s.runs)
	h("GET /api/tickets/{id}/runs/{runId}/events", s.runEvents)
	h("POST /api/tickets/{id}/preview", s.startPreview)
	h("DELETE /api/preview", s.stopPreview)
	h("GET /api/artifacts/{aid}", s.artifact)

	h("GET /api/accounts", s.listAccounts)
	h("POST /api/accounts", s.createAccount)
	h("PATCH /api/accounts/{aid}", s.patchAccount)
	h("DELETE /api/accounts/{aid}", s.deleteAccount)
	h("GET /api/settings", s.getSettings)
	h("PUT /api/settings", s.putSettings)

	h("GET /api/skills", s.listSkills)
	h("GET /api/skills/{name}/files/{file...}", s.readSkillFile)
	h("PUT /api/skills/{name}/files/{file...}", s.writeSkillFile)
	h("DELETE /api/skills/{name}", s.deleteSkill)
	h("GET /api/skills/benchmark", s.benchResults)
	h("POST /api/skills/benchmark", s.startBench)

	h("GET /api/notifications", s.getNotifications)
	h("PUT /api/notifications", s.putNotifications)
	h("POST /api/push/subscribe", s.subscribe)
	h("POST /api/push/test", s.pushTest)

	mux.Handle("/", s.spa())
	return securityHeaders(mux)
}

// securityHeaders sets conservative headers on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		// State-changing requests must come from the app itself: a page on
		// another origin cannot set this header without a CORS preflight,
		// which this server never grants.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && strings.HasPrefix(r.URL.Path, "/api/") &&
			r.Header.Get("X-Requested-With") != "remote-agent" {
			http.Error(w, `{"error":"missing X-Requested-With header"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spa serves the frontend, falling back to index.html for client routes.
func (s *Server) spa() http.Handler {
	if s.SPA == nil {
		return http.NotFoundHandler()
	}
	files := http.FileServerFS(s.SPA)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.SPA, p); err != nil {
			r.URL.Path = "/"
		}
		if p == "sw.js" || p == "index.html" || r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func badRequest(msg string) error { return httpError{http.StatusBadRequest, msg} }

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	code := http.StatusInternalServerError
	var he httpError
	switch {
	case errors.As(err, &he):
		code = he.code
	case errors.Is(err, store.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, orchestrator.ErrInvalid), errors.Is(err, skills.ErrInvalid):
		code = http.StatusBadRequest
	case errors.Is(err, orchestrator.ErrNotActive), errors.Is(err, orchestrator.ErrBusy):
		code = http.StatusConflict
	}
	if code == http.StatusInternalServerError {
		s.Log.Error("api error", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON body: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, badRequest("bad " + name)
	}
	return id, nil
}
