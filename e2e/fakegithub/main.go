// Command fakegithub is an in-memory stand-in for GitHub used by the e2e
// smoke test. It serves smart-HTTP git (via git http-backend) under /git/,
// the REST and GraphQL calls remote-agent makes under /api/, and a control
// API under /_control/ for the test to set CI results and inspect state.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type user struct {
	Login string `json:"login"`
}

type ref struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	StateReason string    `json:"state_reason,omitempty"`
	HTMLURL     string    `json:"html_url"`
	User        user      `json:"user"`
	Labels      []any     `json:"labels"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

type pull struct {
	Number         int        `json:"number"`
	NodeID         string     `json:"node_id"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	State          string     `json:"state"`
	Draft          bool       `json:"draft"`
	Merged         bool       `json:"merged"`
	HTMLURL        string     `json:"html_url"`
	User           user       `json:"user"`
	Head           ref        `json:"head"`
	Base           ref        `json:"base"`
	Mergeable      *bool      `json:"mergeable"`
	MergeableState string     `json:"mergeable_state"`
	MergedAt       *time.Time `json:"merged_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	User      user      `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Issue     int       `json:"issue"`
}

type repo struct {
	Name     string           `json:"name"`
	Issues   map[int]*issue   `json:"issues"`
	Pulls    map[int]*pull    `json:"pulls"`
	Comments []*comment       `json:"comments"`
	CI       string           `json:"ci"` // conclusion given to every head: success | failure | pending
	Merges   []map[string]any `json:"merges"`
	next     int
}

type server struct {
	root string // bare repositories, <root>/<owner>/<name>.git
	git  http.Handler
	base string

	mu        sync.Mutex
	repos     map[string]*repo
	commentID int64
}

func main() {
	listen := flag.String("listen", "127.0.0.1:17800", "listen address")
	root := flag.String("root", "", "directory for bare repositories")
	seed := flag.String("seed", "", "comma-separated owner/name repos to create with one commit on main")
	flag.Parse()
	if *root == "" {
		log.Fatal("-root is required")
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		log.Fatal(err)
	}
	s := &server{
		root:  *root,
		base:  "http://" + *listen,
		repos: map[string]*repo{},
		git: &cgi.Handler{
			Path: gitBin, Args: []string{"http-backend"}, Root: "/git",
			Env: []string{"GIT_PROJECT_ROOT=" + *root, "GIT_HTTP_EXPORT_ALL=1"},
		},
	}
	for _, name := range strings.Split(*seed, ",") {
		if name = strings.TrimSpace(name); name != "" {
			if err := s.create(name); err != nil {
				log.Fatalf("seed %s: %v", name, err)
			}
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/git/", s.git)
	mux.HandleFunc("/api/", s.api)
	mux.HandleFunc("GET /_control/state", s.state)
	mux.HandleFunc("POST /_control/ci", s.setCI)
	log.Printf("fakegithub on %s", s.base)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fake", "GIT_AUTHOR_EMAIL=fake@example.invalid",
		"GIT_COMMITTER_NAME=fake", "GIT_COMMITTER_EMAIL=fake@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// create makes a bare repository with one commit on main.
func (s *server) create(name string) error {
	bare := filepath.Join(s.root, name+".git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		return err
	}
	if _, err := run(bare, "init", "-q", "--bare", "-b", "main"); err != nil {
		return err
	}
	if _, err := run(bare, "config", "http.receivepack", "true"); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "fakegithub-seed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if _, err := run(work, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "initial"}, {"push", "-q", bare, "main"}} {
		if _, err := run(work, args...); err != nil {
			return err
		}
	}
	s.repos[name] = &repo{Name: name, Issues: map[int]*issue{}, Pulls: map[int]*pull{}, CI: "success", next: 1}
	return nil
}

func (s *server) headSHA(name, branch string) string {
	sha, err := run(filepath.Join(s.root, name+".git"), "rev-parse", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return sha
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}

func (s *server) state(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.repos)
}

func (s *server) setCI(w http.ResponseWriter, r *http.Request) {
	var in struct{ Repo, Conclusion string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repos[in.Repo]
	if rp == nil {
		notFound(w)
		return
	}
	rp.CI = in.Conclusion
	writeJSON(w, http.StatusOK, rp)
}

// api serves /api/repos/{owner}/{name}/... and /api/graphql.
func (s *server) api(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api")
	if path == "/graphql" {
		s.graphql(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/repos/"), "/")
	if !strings.HasPrefix(path, "/repos/") || len(parts) < 2 {
		notFound(w)
		return
	}
	name := parts[0] + "/" + parts[1]
	rp := s.repos[name]
	if rp == nil {
		notFound(w)
		return
	}
	rest := parts[2:]
	now := time.Now().UTC()
	var body map[string]any
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodPut) {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	str := func(k string) string { v, _ := body[k].(string); return v }
	num := func(i int) int {
		if i >= len(rest) {
			return 0
		}
		n, _ := strconv.Atoi(rest[i])
		return n
	}
	url := func(kind string, n int) string { return fmt.Sprintf("https://github.com/%s/%s/%d", name, kind, n) }

	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"full_name": name, "default_branch": "main", "html_url": "https://github.com/" + name,
			"allow_squash_merge": true, "allow_merge_commit": true, "allow_rebase_merge": true,
		})

	case len(rest) == 1 && rest[0] == "issues" && r.Method == http.MethodPost:
		n := rp.next
		rp.next++
		is := &issue{Number: n, Title: str("title"), Body: str("body"), State: "open", HTMLURL: url("issues", n),
			User: user{"remote-agent"}, Labels: []any{}, CreatedAt: now, UpdatedAt: now}
		rp.Issues[n] = is
		writeJSON(w, http.StatusCreated, is)
	case len(rest) == 1 && rest[0] == "issues" && r.Method == http.MethodGet:
		out := []*issue{}
		for _, is := range rp.Issues {
			if is.State == "open" {
				out = append(out, is)
			}
		}
		writeJSON(w, http.StatusOK, out)
	case len(rest) == 2 && rest[0] == "issues" && rest[1] != "comments":
		is := rp.Issues[num(1)]
		if is == nil {
			notFound(w)
			return
		}
		if r.Method == http.MethodPatch {
			if v, ok := body["body"].(string); ok {
				is.Body = v
			}
			if v, ok := body["title"].(string); ok {
				is.Title = v
			}
			if v, ok := body["state"].(string); ok {
				is.State = v
			}
			if v, ok := body["state_reason"].(string); ok {
				is.StateReason = v
			}
			is.UpdatedAt = now
		}
		writeJSON(w, http.StatusOK, is)
	case len(rest) == 3 && rest[0] == "issues" && rest[2] == "comments":
		n := num(1)
		if r.Method == http.MethodPost {
			s.commentID++
			c := &comment{ID: s.commentID, Body: str("body"), HTMLURL: url("issues", n), User: user{"remote-agent"},
				CreatedAt: now, UpdatedAt: now, Issue: n}
			rp.Comments = append(rp.Comments, c)
			writeJSON(w, http.StatusCreated, c)
			return
		}
		out := []*comment{}
		for _, c := range rp.Comments {
			if c.Issue == n {
				out = append(out, c)
			}
		}
		writeJSON(w, http.StatusOK, out)
	case len(rest) == 3 && rest[0] == "issues" && rest[1] == "comments" && r.Method == http.MethodPatch:
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		for _, c := range rp.Comments {
			if c.ID == id {
				c.Body, c.UpdatedAt = str("body"), now
				writeJSON(w, http.StatusOK, c)
				return
			}
		}
		notFound(w)

	case len(rest) == 1 && rest[0] == "pulls" && r.Method == http.MethodPost:
		head := str("head")
		sha := s.headSHA(name, head)
		if sha == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "head branch " + head + " does not exist"})
			return
		}
		n := rp.next
		rp.next++
		draft, _ := body["draft"].(bool)
		yes := true
		p := &pull{Number: n, NodeID: fmt.Sprintf("PR_%d", n), Title: str("title"), Body: str("body"), State: "open",
			Draft: draft, HTMLURL: url("pull", n), User: user{"remote-agent"}, Head: ref{head, sha},
			Base: ref{str("base"), s.headSHA(name, str("base"))}, Mergeable: &yes, MergeableState: "clean",
			CreatedAt: now, UpdatedAt: now}
		rp.Pulls[n] = p
		rp.Issues[n] = &issue{Number: n, Title: p.Title, State: "open", HTMLURL: p.HTMLURL, Labels: []any{},
			CreatedAt: now, UpdatedAt: now, PullRequest: &struct{}{}}
		writeJSON(w, http.StatusCreated, p)
	case len(rest) == 1 && rest[0] == "pulls" && r.Method == http.MethodGet:
		want := r.URL.Query().Get("state")
		out := []*pull{}
		for _, p := range rp.Pulls {
			if p.State == want {
				p.Head.SHA = s.headSHA(name, p.Head.Ref)
				out = append(out, p)
			}
		}
		writeJSON(w, http.StatusOK, out)
	case len(rest) == 2 && rest[0] == "pulls":
		p := rp.Pulls[num(1)]
		if p == nil {
			notFound(w)
			return
		}
		if !p.Merged {
			p.Head.SHA = s.headSHA(name, p.Head.Ref)
		}
		if r.Method == http.MethodPatch {
			if v, ok := body["body"].(string); ok {
				p.Body = v
			}
			if v, ok := body["title"].(string); ok {
				p.Title = v
			}
			if v, ok := body["state"].(string); ok {
				if p.Merged {
					writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "merged pull requests cannot change state"})
					return
				}
				p.State = v
			}
			p.UpdatedAt = now
		}
		writeJSON(w, http.StatusOK, p)
	case len(rest) == 3 && rest[0] == "pulls" && rest[2] == "merge" && r.Method == http.MethodPut:
		p := rp.Pulls[num(1)]
		if p == nil {
			notFound(w)
			return
		}
		p.Head.SHA = s.headSHA(name, p.Head.Ref)
		if sha := str("sha"); sha != "" && sha != p.Head.SHA {
			writeJSON(w, http.StatusConflict, map[string]string{"message": "Head branch was modified"})
			return
		}
		if p.Draft {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"message": "Pull request is in draft state"})
			return
		}
		if _, err := run(filepath.Join(s.root, name+".git"), "update-ref", "refs/heads/"+p.Base.Ref, p.Head.SHA); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
			return
		}
		p.Merged, p.State, p.MergedAt, p.UpdatedAt = true, "closed", &now, now
		rp.Merges = append(rp.Merges, map[string]any{"number": p.Number, "sha": p.Head.SHA, "method": str("merge_method")})
		if is := rp.Issues[p.Number]; is != nil {
			is.State = "closed"
		}
		writeJSON(w, http.StatusOK, map[string]any{"sha": p.Head.SHA, "merged": true, "message": "merged"})

	case len(rest) == 3 && rest[0] == "commits" && strings.HasPrefix(rest[2], "check-runs"):
		status, conclusion := "completed", rp.CI
		if rp.CI == "pending" {
			status, conclusion = "in_progress", ""
		}
		id := int64(len(rest[1]))
		writeJSON(w, http.StatusOK, map[string]any{"total_count": 1, "check_runs": []map[string]any{{
			"id": id, "name": "ci", "status": status, "conclusion": conclusion,
			"html_url": "https://github.com/" + name + "/actions", "output": map[string]string{"title": "ci", "summary": "fake CI " + rp.CI},
		}}})
	case len(rest) == 3 && rest[0] == "commits" && rest[2] == "status":
		writeJSON(w, http.StatusOK, map[string]any{"state": "success", "statuses": []any{}})
	case len(rest) == 2 && rest[0] == "check-runs":
		writeJSON(w, http.StatusOK, map[string]any{"id": num(1), "name": "ci", "status": "completed", "conclusion": rp.CI,
			"output": map[string]string{"title": "ci", "summary": "fake CI " + rp.CI, "text": "FAIL: TestSomething"}})
	default:
		notFound(w)
	}
}

func (s *server) graphql(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, _ := in.Variables["id"].(string)
	if strings.Contains(in.Query, "markPullRequestReadyForReview") {
		for _, rp := range s.repos {
			for _, p := range rp.Pulls {
				if p.NodeID == id {
					p.Draft = false
					writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
						"markPullRequestReadyForReview": map[string]any{"pullRequest": map[string]any{"isDraft": false}}}})
					return
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"errors": []map[string]string{{"message": "unsupported query"}}})
}
