package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1n7ax/remote-agent/internal/config"
	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/orchestrator"
	"github.com/s1n7ax/remote-agent/internal/store"
)

func TestImageUploadEndpoints(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	o := orchestrator.New(orchestrator.Options{Config: cfg, Store: st, GitHub: issuesOnly{}})
	p, err := st.CreateProject(ctx, domain.Project{Name: "d", Repo: "a/d", DefaultBranch: "main", Agent: domain.AgentFake})
	if err != nil {
		t.Fatal(err)
	}
	a := &Auth{Token: "tok", Secret: []byte("s")}
	h := (&Server{Auth: a, Orch: o, Store: st}).Handler()

	do := func(method, path string, body []byte, ctype string) (int, map[string]any) {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("X-Requested-With", "remote-agent")
		r.Header.Set("Authorization", "Bearer tok")
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	form := func(name string, content []byte) ([]byte, string) {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write(content)
		_ = mw.Close()
		return b.Bytes(), mw.FormDataContentType()
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

	body, ct := form("shot.png", png)
	code, out := do("POST", "/api/projects/"+itoa(p.ID)+"/uploads", body, ct)
	if code != http.StatusCreated || out["id"] == nil {
		t.Fatalf("upload = %d %v", code, out)
	}
	body, _ = json.Marshal(map[string]any{"idea": "fix it", "agent": "fake", "images": []any{out["id"]}})
	code, out = do("POST", "/api/projects/"+itoa(p.ID)+"/tickets", body, "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create ticket = %d %v", code, out)
	}
	tid := itoa(int64(out["id"].(float64)))

	body, ct = form("doc.pdf", []byte("%PDF-1.7 hello"))
	if code, out = do("POST", "/api/tickets/"+tid+"/images", body, ct); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "only images") {
		t.Fatalf("pdf = %d %v", code, out)
	}
	body, ct = form("big.png", append(png, make([]byte, domain.MaxImageSize+2<<20)...))
	if code, out = do("POST", "/api/tickets/"+tid+"/images", body, ct); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "20 MB") {
		t.Fatalf("oversized = %d %v", code, out)
	}
	body, ct = form("b.png", png)
	if code, out = do("POST", "/api/tickets/"+tid+"/images", body, ct); code != http.StatusCreated || out["n"] != float64(2) {
		t.Fatalf("add image = %d %v", code, out)
	}
	if code, out = do("DELETE", "/api/tickets/"+tid+"/images/1", nil, ""); code != http.StatusOK {
		t.Fatalf("remove = %d %v", code, out)
	}
	code, out = do("GET", "/api/tickets/"+tid, nil, "")
	ims, _ := out["images"].([]any)
	if code != http.StatusOK || len(ims) != 1 || ims[0].(map[string]any)["n"] != float64(2) {
		t.Fatalf("detail images = %d %v", code, out["images"])
	}
	art := ims[0].(map[string]any)["artifactId"].(string)
	r := httptest.NewRequest("GET", "/api/artifacts/"+art, nil)
	r.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("artifact = %d %s", w.Code, w.Header().Get("Content-Type"))
	}

	// Deleting the project removes its image files and pending uploads.
	body, ct = form("pending.png", png)
	_, out = do("POST", "/api/projects/"+itoa(p.ID)+"/uploads", body, ct)
	pending := out["id"].(string)
	if code, out = do("DELETE", "/api/projects/"+itoa(p.ID), nil, ""); code != http.StatusOK {
		t.Fatalf("delete project = %d %v", code, out)
	}
	for _, id := range []string{art, pending} {
		if _, err := os.Stat(o.ArtifactPath(id)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file %s survived its project: %v", id, err)
		}
	}
}

// issuesOnly is a GitHub with just the issue calls ticket creation makes.
type issuesOnly struct{ orchestrator.GitHub }

func (issuesOnly) CreateIssue(context.Context, string, string, string) (int, error) { return 7, nil }

func (issuesOnly) UpdateIssueBody(context.Context, string, int, string) error { return nil }

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
