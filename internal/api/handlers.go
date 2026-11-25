package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/orchestrator"
	"github.com/s1n7ax/remote-agent/internal/store"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.Version})
}

func (s *Server) idle(w http.ResponseWriter, r *http.Request) error {
	return ok(w, map[string]any{"idle": s.Orch.Idle(), "activeRuns": s.Orch.ActiveRuns(), "draining": s.Orch.Draining()})
}

func (s *Server) drain(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Drain bool `json:"drain"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	s.Orch.Drain(in.Drain)
	return s.idle(w, r)
}

// Model choices shown as one-tap chips. Pi models are OpenRouter ids.
var models = map[domain.AgentKind][]string{
	domain.AgentClaude: {"opus", "sonnet", "haiku", "fable"},
	domain.AgentPi:     {"anthropic/claude-sonnet-latest", "deepseek/deepseek-v4.1-flash", "openai/gpt-5.5", "google/gemini-3-pro"},
}

var efforts = map[domain.AgentKind][]string{
	domain.AgentClaude: {"low", "medium", "high", "xhigh", "max"},
	domain.AgentPi:     {"off", "low", "medium", "high", "xhigh"},
}

// SettingPiModels holds a comma-separated override of the pi model chips.
const SettingPiModels = "pi_models"

func (s *Server) state(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	projects, err := s.Store.Projects(ctx)
	if err != nil {
		return err
	}
	accounts, err := s.Store.Accounts(ctx)
	if err != nil {
		return err
	}
	settings, err := s.settings(ctx)
	if err != nil {
		return err
	}
	m := map[domain.AgentKind][]string{domain.AgentClaude: models[domain.AgentClaude], domain.AgentPi: models[domain.AgentPi]}
	if v, found, _ := s.Store.Setting(ctx, SettingPiModels); found && strings.TrimSpace(v) != "" {
		m[domain.AgentPi] = splitList(v)
	}
	vapid := ""
	if s.Push != nil {
		vapid = s.Push.PublicKey()
	}
	return ok(w, map[string]any{
		"version": s.Version, "projects": projects, "accounts": accounts, "settings": settings,
		"models": m, "efforts": efforts, "vapidPublicKey": vapid, "preview": s.Orch.Preview(),
		"activeRuns": s.Orch.ActiveRuns(), "draining": s.Orch.Draining(),
		"statuses": domain.Statuses, "notifyEvents": domain.NotifyEvents,
	})
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// events streams live updates as server-sent events.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, okf := w.(http.Flusher)
	if !okf {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsub := s.Orch.Bus().Subscribe()
	defer unsub()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case m, open := <-ch:
			if !open {
				return
			}
			raw, err := json.Marshal(m)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.Type, raw)
			flusher.Flush()
		}
	}
}

// --- projects ---

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) error {
	ps, err := s.Store.Projects(r.Context())
	if err != nil {
		return err
	}
	return ok(w, ps)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) error {
	var p domain.Project
	if err := decode(r, &p); err != nil {
		return err
	}
	p.Repo = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(p.Repo), "https://github.com/"), ".git")
	if parts := strings.Split(p.Repo, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return badRequest("repo must be owner/name")
	}
	if p.Name == "" {
		p.Name = strings.Split(p.Repo, "/")[1]
	}
	if p.Agent == "" {
		p.Agent = domain.AgentClaude
	}
	if p.DefaultBranch == "" {
		branch, err := s.GitHub.DefaultBranch(r.Context(), p.Repo)
		if err != nil {
			return badRequest("cannot read the repository on GitHub: " + err.Error())
		}
		p.DefaultBranch = branch
	}
	p, err := s.Store.CreateProject(r.Context(), p)
	if err != nil {
		return err
	}
	return created(w, p)
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.Store.Project(r.Context(), id)
	if err != nil {
		return err
	}
	var in struct {
		Name          *string           `json:"name"`
		DefaultBranch *string           `json:"defaultBranch"`
		Agent         *domain.AgentKind `json:"agent"`
		Model         *string           `json:"model"`
		Effort        *string           `json:"effort"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	setIf(&p.Name, in.Name)
	setIf(&p.DefaultBranch, in.DefaultBranch)
	setIf(&p.Model, in.Model)
	setIf(&p.Effort, in.Effort)
	setIf(&p.Agent, in.Agent)
	if err := s.Store.UpdateProject(r.Context(), p); err != nil {
		return err
	}
	return ok(w, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteProject(r.Context(), id); err != nil {
		return err
	}
	return ok(w, map[string]bool{"deleted": true})
}

func (s *Server) board(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ts, err := s.Store.ProjectTickets(r.Context(), id)
	if err != nil {
		return err
	}
	cols := map[domain.Status][]domain.Ticket{}
	for _, st := range domain.Statuses {
		cols[st] = []domain.Ticket{}
	}
	for _, t := range ts {
		cols[t.Status] = append(cols[t.Status], t)
	}
	return ok(w, map[string]any{"projectId": id, "columns": cols})
}

func (s *Server) createTicket(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in orchestrator.NewTicket
	if err := decode(r, &in); err != nil {
		return err
	}
	t, err := s.Orch.CreateTicket(r.Context(), id, in)
	if err != nil {
		return err
	}
	return created(w, t)
}

// --- tickets ---

func (s *Server) ticket(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	t, err := s.Store.Ticket(ctx, id)
	if err != nil {
		return err
	}
	rounds, err := s.Store.Rounds(ctx, id)
	if err != nil {
		return err
	}
	runs, err := s.Store.TicketRuns(ctx, id)
	if err != nil {
		return err
	}
	protos, err := s.Store.Prototypes(ctx, id)
	if err != nil {
		return err
	}
	out := map[string]any{"ticket": t, "rounds": rounds, "runs": runs, "prototypes": protos}
	if proof, found, err := s.Store.LatestProof(ctx, id); err != nil {
		return err
	} else if found {
		out["proof"] = proof
	}
	if pv := s.Orch.Preview(); pv != nil && pv.TicketID == id {
		out["preview"] = pv
	}
	return ok(w, out)
}

func (s *Server) patchTicket(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var p orchestrator.TicketPatch
	if err := decode(r, &p); err != nil {
		return err
	}
	t, err := s.Orch.PatchTicket(r.Context(), id, p)
	if err != nil {
		return err
	}
	return ok(w, t)
}

func (s *Server) deleteTicket(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Orch.DeleteTicket(r.Context(), id); err != nil {
		return err
	}
	return ok(w, map[string]bool{"deleted": true})
}

func (s *Server) ticketAction(action string) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := pathID(r, "id")
		if err != nil {
			return err
		}
		switch action {
		case "start":
			_, err = s.Orch.StartTicket(ctx, id)
		case "retry":
			_, err = s.Orch.RetryTicket(ctx, id)
		case "pause":
			err = s.Orch.Pause(ctx, id)
		case "resume":
			err = s.Orch.Resume(ctx, id)
		case "stop":
			err = s.Orch.Stop(ctx, id)
		}
		if err != nil {
			return err
		}
		t, err := s.Store.Ticket(ctx, id)
		if err != nil {
			return err
		}
		return ok(w, t)
	}
}

func (s *Server) message(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Text) == "" {
		return badRequest("empty message")
	}
	if err := s.Orch.SendMessage(r.Context(), id, in.Text); err != nil {
		return err
	}
	return ok(w, map[string]bool{"sent": true})
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		return badRequest("bad round")
	}
	var a domain.Answer
	if err := decode(r, &a); err != nil {
		return err
	}
	res, err := s.Orch.Answer(r.Context(), id, n, r.PathValue("qid"), a)
	if err != nil {
		return err
	}
	return ok(w, res)
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	runs, err := s.Store.TicketRuns(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, runs)
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request) error {
	ticketID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	runID, err := pathID(r, "runId")
	if err != nil {
		return err
	}
	run, err := s.Store.Run(r.Context(), runID)
	if err != nil {
		return err
	}
	if run.TicketID != ticketID {
		return store.ErrNotFound
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	evs, err := s.Store.Events(r.Context(), runID, after, limit)
	if err != nil {
		return err
	}
	return ok(w, evs)
}

func (s *Server) startPreview(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Prototype string `json:"prototype"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	pv, err := s.Orch.StartPrototypePreview(r.Context(), id, in.Prototype)
	if err != nil {
		return err
	}
	return ok(w, pv)
}

func (s *Server) stopPreview(w http.ResponseWriter, r *http.Request) error {
	s.Orch.StopPreview()
	return ok(w, map[string]bool{"stopped": true})
}

func (s *Server) artifact(w http.ResponseWriter, r *http.Request) error {
	a, err := s.Store.Artifact(r.Context(), r.PathValue("aid"))
	if err != nil {
		return err
	}
	f, err := os.Open(s.Orch.ArtifactPath(a.ID))
	if err != nil {
		return store.ErrNotFound
	}
	defer f.Close()
	mt := a.MIME
	// Never let an uploaded HTML or SVG run script on the app's origin.
	if strings.HasPrefix(mt, "text/html") || strings.HasPrefix(mt, "image/svg") || mt == "" {
		mt = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", a.Name))
	http.ServeContent(w, r, a.Name, a.CreatedAt, f)
	return nil
}

// --- GitHub view ---

func (s *Server) projectGitHub(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.Store.Project(ctx, id)
	if err != nil {
		return err
	}
	pulls, err := s.GitHub.ListPulls(ctx, p.Repo)
	if err != nil {
		return fmt.Errorf("list pulls: %w", err)
	}
	issues, err := s.GitHub.ListIssues(ctx, p.Repo)
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}
	tickets, err := s.Store.ProjectTickets(ctx, id)
	if err != nil {
		return err
	}
	byPR := map[int]int64{}
	for _, t := range tickets {
		if t.PR != 0 {
			byPR[t.PR] = t.ID
		}
	}
	sort.Slice(pulls, func(i, j int) bool { return pulls[i].Number > pulls[j].Number })
	return ok(w, map[string]any{"repo": p.Repo, "pulls": pulls, "issues": issues, "ticketsByPr": byPR})
}

func (s *Server) merge(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		return badRequest("bad PR number")
	}
	var in struct {
		Method string `json:"method"`
		// SHA is the head the user reviewed; a newer push makes the merge fail.
		SHA string `json:"sha"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			return err
		}
	}
	if in.Method != "" && in.Method != "merge" && in.Method != "squash" && in.Method != "rebase" {
		return badRequest("method must be merge, squash or rebase")
	}
	if err := s.Orch.MergeAt(r.Context(), id, n, in.Method, in.SHA); err != nil {
		return err
	}
	return ok(w, map[string]bool{"merged": true})
}

// --- schedules ---

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	scs, err := s.Store.Schedules(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, scs)
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var sc domain.Schedule
	if err := decode(r, &sc); err != nil {
		return err
	}
	sc.ProjectID = id
	if err := validSchedule(&sc); err != nil {
		return err
	}
	sc, err = s.Store.CreateSchedule(r.Context(), sc)
	if err != nil {
		return err
	}
	return created(w, sc)
}

func validSchedule(sc *domain.Schedule) error {
	if strings.TrimSpace(sc.Name) == "" || strings.TrimSpace(sc.Prompt) == "" {
		return badRequest("a schedule needs a name and a prompt")
	}
	next, err := orchestrator.NextRun(sc.Cron, time.Now())
	if err != nil {
		return badRequest(err.Error())
	}
	sc.NextRunAt = &next
	if sc.Agent == "" {
		sc.Agent = domain.AgentClaude
	}
	return nil
}

func (s *Server) patchSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	sc, err := s.Store.Schedule(r.Context(), id)
	if err != nil {
		return err
	}
	var in struct {
		Name    *string           `json:"name"`
		Cron    *string           `json:"cron"`
		Prompt  *string           `json:"prompt"`
		Agent   *domain.AgentKind `json:"agent"`
		Model   *string           `json:"model"`
		Effort  *string           `json:"effort"`
		Enabled *bool             `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	setIf(&sc.Name, in.Name)
	setIf(&sc.Cron, in.Cron)
	setIf(&sc.Prompt, in.Prompt)
	setIf(&sc.Agent, in.Agent)
	setIf(&sc.Model, in.Model)
	setIf(&sc.Effort, in.Effort)
	setIf(&sc.Enabled, in.Enabled)
	if err := validSchedule(&sc); err != nil {
		return err
	}
	if err := s.Store.UpdateSchedule(r.Context(), sc); err != nil {
		return err
	}
	return ok(w, sc)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "sid")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteSchedule(r.Context(), id); err != nil {
		return err
	}
	return ok(w, map[string]bool{"deleted": true})
}

// --- accounts & settings ---

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) error {
	accs, err := s.Store.Accounts(r.Context())
	if err != nil {
		return err
	}
	return ok(w, accs)
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Label    string `json:"label"`
		Token    string `json:"token"`
		Priority int    `json:"priority"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Token = strings.TrimSpace(in.Token)
	if in.Label == "" || in.Token == "" {
		return badRequest("an account needs a label and the token from `claude setup-token`")
	}
	if strings.HasPrefix(in.Token, "sk-ant-api") {
		return badRequest("that is an API key; paste the subscription token from `claude setup-token`")
	}
	a, err := s.Store.CreateAccount(r.Context(), domain.Account{Label: in.Label, Priority: in.Priority}, in.Token)
	if err != nil {
		return err
	}
	s.Orch.Kick()
	return created(w, a)
}

func (s *Server) patchAccount(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "aid")
	if err != nil {
		return err
	}
	a, err := s.Store.Account(r.Context(), id)
	if err != nil {
		return err
	}
	var in struct {
		Label        *string `json:"label"`
		Priority     *int    `json:"priority"`
		ClearLimited bool    `json:"clearLimited"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	setIf(&a.Label, in.Label)
	setIf(&a.Priority, in.Priority)
	if in.ClearLimited {
		a.ExhaustedTill, a.Warning = nil, false
	}
	if err := s.Store.UpdateAccount(r.Context(), a); err != nil {
		return err
	}
	s.Orch.Kick()
	return ok(w, a)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "aid")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAccount(r.Context(), id); err != nil {
		return err
	}
	return ok(w, map[string]bool{"deleted": true})
}

// Settings is the user-editable settings document.
type Settings struct {
	MaxParallelRuns      int      `json:"maxParallelRuns"`
	DailyCapUSD          float64  `json:"dailyCapUsd"`
	TicketCapUSD         float64  `json:"ticketCapUsd"`
	SpentTodayUSD        float64  `json:"spentTodayUsd"`
	OpenRouterConfigured bool     `json:"openrouterConfigured"`
	PiModels             []string `json:"piModels"`
}

func (s *Server) settings(ctx context.Context) (Settings, error) {
	st := Settings{
		DailyCapUSD:  floatSetting(ctx, s.Store, orchestrator.SettingDailyCap),
		TicketCapUSD: floatSetting(ctx, s.Store, orchestrator.SettingTicketCap),
	}
	if s.MaxParallel != nil {
		st.MaxParallelRuns = s.MaxParallel()
	}
	if s.OpenRouterConfigured != nil {
		st.OpenRouterConfigured = s.OpenRouterConfigured()
	}
	spent, err := s.Store.Spend(ctx, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		return st, err
	}
	st.SpentTodayUSD = spent
	if v, found, _ := s.Store.Setting(ctx, SettingPiModels); found {
		st.PiModels = splitList(v)
	}
	return st, nil
}

func floatSetting(ctx context.Context, st *store.Store, key string) float64 {
	v, found, err := st.Setting(ctx, key)
	if err != nil || !found {
		return 0
	}
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := s.settings(r.Context())
	if err != nil {
		return err
	}
	return ok(w, st)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var in struct {
		DailyCapUSD  *float64 `json:"dailyCapUsd"`
		TicketCapUSD *float64 `json:"ticketCapUsd"`
		PiModels     []string `json:"piModels"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	for key, v := range map[string]*float64{orchestrator.SettingDailyCap: in.DailyCapUSD, orchestrator.SettingTicketCap: in.TicketCapUSD} {
		if v == nil {
			continue
		}
		if *v < 0 {
			return badRequest("caps cannot be negative")
		}
		if err := s.Store.SetSetting(ctx, key, strconv.FormatFloat(*v, 'f', 2, 64)); err != nil {
			return err
		}
	}
	if in.PiModels != nil {
		if err := s.Store.SetSetting(ctx, SettingPiModels, strings.Join(in.PiModels, ",")); err != nil {
			return err
		}
	}
	return s.getSettings(w, r)
}

// --- skills ---

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Skills.List(r.Context())
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) readSkillFile(w http.ResponseWriter, r *http.Request) error {
	content, err := s.Skills.Read(r.PathValue("name"), r.PathValue("file"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store.ErrNotFound
		}
		return err
	}
	return ok(w, map[string]string{"content": content})
}

func (s *Server) writeSkillFile(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Content string `json:"content"`
		Message string `json:"message"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	sha, err := s.Skills.Write(r.Context(), r.PathValue("name"), r.PathValue("file"), in.Content, in.Message)
	if err != nil {
		return err
	}
	return ok(w, map[string]string{"commit": sha})
}

func (s *Server) deleteSkill(w http.ResponseWriter, r *http.Request) error {
	sha, err := s.Skills.Delete(r.Context(), r.PathValue("name"), "")
	if err != nil {
		return err
	}
	return ok(w, map[string]string{"commit": sha})
}

func (s *Server) benchResults(w http.ResponseWriter, r *http.Request) error {
	res, err := s.Bench.Results()
	if err != nil {
		return err
	}
	return ok(w, res)
}

func (s *Server) startBench(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Skills []string `json:"skills"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			return err
		}
	}
	started, err := s.Bench.Start(r.Context(), in.Skills)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": started})
	return nil
}

// --- notifications ---

func (s *Server) getNotifications(w http.ResponseWriter, r *http.Request) error {
	prefs, err := s.Push.Prefs(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"prefs": prefs, "vapidPublicKey": s.Push.PublicKey()})
}

func (s *Server) putNotifications(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Prefs map[string]bool `json:"prefs"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := s.Push.SetPrefs(r.Context(), in.Prefs); err != nil {
		return err
	}
	return s.getNotifications(w, r)
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) error {
	var sub json.RawMessage
	if err := decode(r, &sub); err != nil {
		return err
	}
	if err := s.Push.Subscribe(r.Context(), sub); err != nil {
		return err
	}
	return ok(w, map[string]bool{"subscribed": true})
}

func (s *Server) pushTest(w http.ResponseWriter, r *http.Request) error {
	if err := s.Push.Test(r.Context()); err != nil {
		return err
	}
	return ok(w, map[string]bool{"sent": true})
}
