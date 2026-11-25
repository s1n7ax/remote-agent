package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/questions"
)

// ErrInvalid wraps a request the user (or agent) got wrong.
var ErrInvalid = errors.New("invalid request")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// NewTicket is what the user gives to create a ticket.
type NewTicket struct {
	Idea       string           `json:"idea"`
	Title      string           `json:"title"`
	Agent      domain.AgentKind `json:"agent"`
	Model      string           `json:"model"`
	Effort     string           `json:"effort"`
	Prototypes bool             `json:"prototypes"`
	Start      bool             `json:"start"`
	StartAt    *time.Time       `json:"startAt"`
}

// titleFrom derives a short title from the idea's first line.
func titleFrom(idea string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(idea), "\n", 2)[0])
	if r := []rune(line); len(r) > 72 {
		line = strings.TrimSpace(string(r[:71])) + "…"
	}
	return line
}

// CreateTicket creates a ticket and its GitHub issue.
func (o *Orchestrator) CreateTicket(ctx context.Context, projectID int64, in NewTicket) (domain.Ticket, error) {
	if strings.TrimSpace(in.Idea) == "" {
		return domain.Ticket{}, invalid("the idea is empty")
	}
	p, err := o.st.Project(ctx, projectID)
	if err != nil {
		return domain.Ticket{}, err
	}
	t := domain.Ticket{
		ProjectID: p.ID, Kind: domain.KindTicket, Idea: strings.TrimSpace(in.Idea), Title: strings.TrimSpace(in.Title),
		Status: domain.StatusBacklog, Stage: domain.StageExplore,
		Agent: in.Agent, Model: in.Model, Effort: in.Effort, Prototypes: in.Prototypes, StartAt: in.StartAt,
		SpendCapUSD: o.DefaultTicketCap(ctx),
	}
	if t.Title == "" {
		t.Title = titleFrom(t.Idea)
	}
	if t.Agent == "" {
		t.Agent, t.Model, t.Effort = p.Agent, p.Model, p.Effort
	}
	if in.Start {
		t.Status = domain.StatusReady
	}
	t, err = o.st.CreateTicket(ctx, t)
	if err != nil {
		return t, err
	}
	body := t.Idea + fmt.Sprintf("\n\n---\n_Tracked by remote-agent (ticket %d)._", t.ID)
	if n, err := o.gh.CreateIssue(ctx, p.Repo, t.Title, body); err != nil {
		o.log.Warn("create issue", "ticket", t.ID, "err", err)
		t.StatusNote = "could not create the GitHub issue: " + err.Error()
	} else {
		t.Issue = n
	}
	issue, note := t.Issue, t.StatusNote
	// The scheduler may have picked the new ticket up already.
	if t, err = o.mutate(ctx, t.ID, func(c *domain.Ticket) {
		c.Issue = issue
		if note != "" {
			c.StatusNote = note
		}
	}); err != nil {
		return t, err
	}
	o.bus.Publish(Message{Type: "board", ProjectID: p.ID})
	o.Kick()
	return t, nil
}

// TicketPatch holds the fields the user may change on a ticket.
type TicketPatch struct {
	Title      *string           `json:"title"`
	Agent      *domain.AgentKind `json:"agent"`
	Model      *string           `json:"model"`
	Effort     *string           `json:"effort"`
	Prototypes *bool             `json:"prototypes"`
	StartAt    *time.Time        `json:"startAt"`
	ClearStart bool              `json:"clearStartAt"`
	SpendCap   *float64          `json:"spendCapUsd"`
}

// PatchTicket applies a patch; agent/model/effort take effect on the next run.
func (o *Orchestrator) PatchTicket(ctx context.Context, id int64, p TicketPatch) (domain.Ticket, error) {
	t, err := o.mutate(ctx, id, func(t *domain.Ticket) {
		if p.Title != nil && strings.TrimSpace(*p.Title) != "" {
			t.Title = strings.TrimSpace(*p.Title)
		}
		if p.Agent != nil {
			t.Agent = *p.Agent
		}
		if p.Model != nil {
			t.Model = *p.Model
		}
		if p.Effort != nil {
			t.Effort = *p.Effort
		}
		if p.Prototypes != nil {
			t.Prototypes = *p.Prototypes
		}
		if p.StartAt != nil {
			t.StartAt = p.StartAt
		}
		if p.ClearStart {
			t.StartAt = nil
		}
		if p.SpendCap != nil {
			t.SpendCapUSD = *p.SpendCap
		}
	})
	if err != nil {
		return t, err
	}
	o.Kick()
	return t, nil
}

// StartTicket queues a backlog (or failed) ticket's next stage now.
func (o *Orchestrator) StartTicket(ctx context.Context, id int64) (domain.Ticket, error) {
	t, err := o.update(ctx, id, func(t *domain.Ticket) error {
		if t.Status != domain.StatusBacklog && t.Status != domain.StatusFailed {
			return invalid("ticket is %s; only backlog or failed tickets can be started", t.Status)
		}
		t.Status = domain.StatusReady
		t.StartAt = nil
		t.StatusNote = ""
		return nil
	})
	if err != nil {
		return t, err
	}
	o.Kick()
	return t, nil
}

// RetryTicket re-runs a failed ticket's stage (resetting fix rounds when it
// failed on them).
func (o *Orchestrator) RetryTicket(ctx context.Context, id int64) (domain.Ticket, error) {
	t, err := o.update(ctx, id, func(t *domain.Ticket) error {
		if t.Status != domain.StatusFailed {
			return invalid("only failed tickets can be retried")
		}
		if t.Stage == domain.StageFix || t.Stage == domain.StageCI {
			t.FixRounds = 0
		}
		if t.Stage == domain.StageCI {
			t.Status = domain.StatusWaiting
		} else {
			t.Status = domain.StatusReady
		}
		t.StatusNote = "retrying"
		return nil
	})
	if err != nil {
		return t, err
	}
	if t.Stage == domain.StageCI {
		o.RecheckCI(t.ID)
	}
	o.Kick()
	return t, nil
}

// AnswerResult says what saving an answer did.
type AnswerResult struct {
	Round     domain.Round  `json:"round"`
	Completed bool          `json:"completed"`
	Ticket    domain.Ticket `json:"ticket"`
}

// Answer saves one answer. When it completes the round, the ticket moves to
// ready (no click needed) and the next run is queued.
func (o *Orchestrator) Answer(ctx context.Context, ticketID int64, n int, qid string, a domain.Answer) (AnswerResult, error) {
	// The round's answers map and the ticket row are read-modify-written
	// under tmu, so concurrent answers do not drop each other, only one of
	// them completes the round, and settle sees either the open round or
	// the resumed ticket.
	o.tmu.Lock()
	res, err := o.answerLocked(ctx, ticketID, n, qid, a)
	o.tmu.Unlock()
	if err != nil || !res.Completed {
		return res, err
	}
	o.postRoundSummary(ctx, res.Ticket, res.Round)
	// The pick is made: a prototype preview of this ticket has done its job.
	if pv := o.Preview(); pv != nil && pv.TicketID == res.Ticket.ID {
		o.StopPreview()
	}
	o.Kick()
	return res, nil
}

// answerLocked is Answer's read-modify-write; the caller holds tmu.
func (o *Orchestrator) answerLocked(ctx context.Context, ticketID int64, n int, qid string, a domain.Answer) (AnswerResult, error) {
	var res AnswerResult
	rd, err := o.st.Round(ctx, ticketID, n)
	if err != nil {
		return res, err
	}
	if rd.CompletedAt != nil {
		return res, invalid("round %d is already complete", n)
	}
	var q *domain.Question
	for i := range rd.Tree.Questions {
		if rd.Tree.Questions[i].ID == qid {
			q = &rd.Tree.Questions[i]
		}
	}
	if q == nil {
		return res, invalid("round %d has no question %q", n, qid)
	}
	if err := checkAnswer(*q, a); err != nil {
		return res, err
	}
	if a.Empty() {
		delete(rd.Answers, qid)
	} else {
		rd.Answers[qid] = a
	}
	t, err := o.st.Ticket(ctx, ticketID)
	if err != nil {
		return res, err
	}
	if questions.Complete(rd.Tree, rd.Answers) {
		now := o.now()
		rd.CompletedAt = &now
		res.Completed = true
	}
	if err := o.st.SaveRound(ctx, rd); err != nil {
		return res, err
	}
	if res.Completed {
		if err := o.roundCompleted(ctx, &t, rd); err != nil {
			return res, err
		}
	}
	res.Round, res.Ticket = rd, t
	return res, nil
}

func checkAnswer(q domain.Question, a domain.Answer) error {
	ids := map[string]bool{}
	for _, opt := range q.Options {
		ids[opt.ID] = true
	}
	if q.Kind == domain.QuestionConfirm {
		ids["yes"] = true
	}
	for _, id := range a.OptionIDs {
		if !ids[id] {
			return invalid("question %q has no option %q", q.ID, id)
		}
	}
	if (q.Kind == domain.QuestionSingle || q.Kind == domain.QuestionConfirm) && len(a.OptionIDs) > 1 {
		return invalid("question %q takes one option", q.ID)
	}
	if a.Text != "" && q.Kind != domain.QuestionText && q.Kind != domain.QuestionConfirm && !q.OtherAllowed() {
		return invalid("question %q does not take a free-text answer", q.ID)
	}
	return nil
}

// roundCompleted resumes a ticket whose open round is now fully answered.
// The caller holds tmu and passes the ticket's current row.
func (o *Orchestrator) roundCompleted(ctx context.Context, t *domain.Ticket, rd domain.Round) error {
	t.StatusNote = ""
	t.Stage = rd.Stage
	t.Status = domain.StatusReady
	if rd.Purpose == domain.PurposeSpendCap {
		cont, err := o.applySpendAnswer(ctx, t, rd.Answers["cap"])
		if err != nil {
			return err
		}
		if !cont {
			t.Status = domain.StatusBacklog
			t.StatusNote = "stopped at the spend cap"
		}
	}
	return o.save(ctx, t)
}

// postRoundSummary mirrors a completed round on the ticket's issue.
func (o *Orchestrator) postRoundSummary(ctx context.Context, t domain.Ticket, rd domain.Round) {
	if rd.Purpose == domain.PurposeSpendCap || t.Issue == 0 {
		return
	}
	p, err := o.st.Project(ctx, t.ProjectID)
	if err != nil {
		return
	}
	marker := fmt.Sprintf("<!-- remote-agent:round:%d -->", rd.N)
	if err := o.gh.UpsertComment(ctx, p.Repo, t.Issue, marker, marker+"\n"+questions.Summary(rd)); err != nil {
		o.log.Warn("post round summary", "ticket", t.ID, "err", err)
	}
}

// OpenRound returns the ticket's open round, if any.
func (o *Orchestrator) OpenRound(ctx context.Context, ticketID int64) (*domain.Round, error) {
	rds, err := o.st.Rounds(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	for i := len(rds) - 1; i >= 0; i-- {
		if rds[i].CompletedAt == nil {
			return &rds[i], nil
		}
	}
	return nil, nil
}

// MergeAt merges a ticket's (or any project's) PR. Only the user's explicit
// merge button reaches this; agents have no path to it. A ticket's PR merges only
// when the commit its verify run tested, the PR's head and the head the user
// reviewed (seenSHA) are one commit; any other PR merges at seenSHA, or at
// its current head when seenSHA is empty.
func (o *Orchestrator) MergeAt(ctx context.Context, projectID int64, number int, method, seenSHA string) error {
	p, err := o.st.Project(ctx, projectID)
	if err != nil {
		return err
	}
	ts, err := o.st.ProjectTickets(ctx, projectID)
	if err != nil {
		return err
	}
	head := seenSHA
	for _, t := range ts {
		if t.PR != number || t.Kind != domain.KindTicket {
			continue
		}
		if t.VerifiedSHA == "" {
			return invalid("PR #%d has no verified commit yet; it can be merged once its ticket is verified", number)
		}
		pr, err := o.gh.Pull(ctx, p.Repo, number)
		if err != nil {
			return err
		}
		if pr.HeadSHA != t.VerifiedSHA {
			o.RecheckCI(t.ID)
			return invalid("PR #%d head %s is not the verified commit %s; it must be verified again before merging",
				number, shortSHA(pr.HeadSHA), shortSHA(t.VerifiedSHA))
		}
		if seenSHA == "" || seenSHA != t.VerifiedSHA {
			return invalid("the PR changed since you looked; review it again (you saw %s, the verified head is %s)",
				shortSHA(seenSHA), shortSHA(t.VerifiedSHA))
		}
		head = t.VerifiedSHA
		break
	}
	if err := o.gh.MergePull(ctx, p.Repo, number, method, head); err != nil {
		return err
	}
	for _, t := range ts {
		if t.PR == number {
			o.RecheckCI(t.ID)
		}
	}
	return nil
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// DeleteTicket stops a ticket's run, removes its worktree and deletes it.
// The GitHub issue and branch stay.
func (o *Orchestrator) DeleteTicket(ctx context.Context, id int64) error {
	t, err := o.st.Ticket(ctx, id)
	if err != nil {
		return err
	}
	if ar := o.activeFor(id); ar != nil {
		return fmt.Errorf("%w: stop it first", ErrBusy)
	}
	o.removeWorkspace(ctx, t)
	if err := o.st.DeleteTicket(ctx, id); err != nil {
		return err
	}
	o.bus.Publish(Message{Type: "board", ProjectID: t.ProjectID})
	return nil
}
