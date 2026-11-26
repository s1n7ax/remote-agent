// Package domain holds the types shared by every part of remote-agent. It does
// no I/O.
package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// Status is a ticket's board column.
type Status string

const (
	StatusBacklog    Status = "backlog"
	StatusNeedsInput Status = "needs_input"
	StatusReady      Status = "ready"
	StatusWorking    Status = "working"
	StatusWaiting    Status = "waiting"
	// StatusReadyForTesting: CI is green; a verify run tests the PR next.
	StatusReadyForTesting Status = "ready_for_testing"
	StatusInReview        Status = "in_review"
	StatusDone            Status = "done"
	StatusFailed          Status = "failed"
)

// Statuses lists every status in board order.
var Statuses = []Status{
	StatusBacklog, StatusNeedsInput, StatusReady, StatusWorking,
	StatusWaiting, StatusReadyForTesting, StatusInReview, StatusDone, StatusFailed,
}

// Stage is the /ship step a ticket runs next.
type Stage string

const (
	StageExplore Stage = "explore"
	StageDesign  Stage = "design"
	StageBuild   Stage = "build"
	StageCI      Stage = "ci"
	StageFix     Stage = "fix"
	StageVerify  Stage = "verify"
	StageMerged  Stage = "merged"
	StageJob     Stage = "job"
)

// Kind tells a ticket from a scheduled job's run.
type Kind string

const (
	KindTicket Kind = "ticket"
	KindJob    Kind = "job"
)

// AgentKind names the harness that runs a ticket.
type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentPi     AgentKind = "pi"
	AgentFake   AgentKind = "fake"
)

// Project is one GitHub repository.
type Project struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Repo          string    `json:"repo"` // owner/name
	DefaultBranch string    `json:"defaultBranch"`
	Agent         AgentKind `json:"agent"`
	Model         string    `json:"model"`
	Effort        string    `json:"effort"`
	// RunTimeoutMinutes stops a run that takes longer; 0 uses the server's
	// default.
	RunTimeoutMinutes int       `json:"runTimeoutMinutes"`
	CreatedAt         time.Time `json:"createdAt"`
}

// Ticket is one card on a project's board.
type Ticket struct {
	ID          int64      `json:"id"`
	ProjectID   int64      `json:"projectId"`
	Kind        Kind       `json:"kind"`
	Title       string     `json:"title"`
	Idea        string     `json:"idea"`
	Status      Status     `json:"status"`
	Stage       Stage      `json:"stage"`
	Agent       AgentKind  `json:"agent"`
	Model       string     `json:"model"`
	Effort      string     `json:"effort"`
	Prototypes  bool       `json:"prototypes"`
	Issue       int        `json:"issue,omitempty"`
	Branch      string     `json:"branch,omitempty"`
	PR          int        `json:"pr,omitempty"`
	VerifiedSHA string     `json:"verifiedSha,omitempty"`
	Requirement string     `json:"requirement,omitempty"`
	Decisions   string     `json:"decisions,omitempty"`
	StatusNote  string     `json:"statusNote,omitempty"`
	StartAt     *time.Time `json:"startAt,omitempty"`
	WaitUntil   *time.Time `json:"waitUntil,omitempty"`
	FixRounds   int        `json:"fixRounds"`
	// FixTrigger says why the ticket went to fix: ci_failed | conflict |
	// verify_failed.
	FixTrigger  string    `json:"fixTrigger,omitempty"`
	SpendUSD    float64   `json:"spendUsd"`
	SpendCapUSD float64   `json:"spendCapUsd"`
	JobPrompt   string    `json:"jobPrompt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// LinkKind is how a linked ticket relates to the ticket it is seen from.
type LinkKind string

const (
	// LinkDependsOn: this ticket runs no stage until the other is done.
	LinkDependsOn LinkKind = "depends_on"
	// LinkBlocks is a dependency seen from the ticket depended on.
	LinkBlocks LinkKind = "blocks"
	// LinkRelated is a two-way cross-reference that holds nothing back.
	LinkRelated LinkKind = "related"
)

// TicketLink is one link as seen from one of its two tickets; the fields
// describe the other ticket.
type TicketLink struct {
	ID         int64    `json:"id"`
	Kind       LinkKind `json:"kind"`
	TicketID   int64    `json:"ticketId"`
	Title      string   `json:"title"`
	Issue      int      `json:"issue,omitempty"`
	Status     Status   `json:"status,omitempty"` // empty once deleted
	StatusNote string   `json:"statusNote,omitempty"`
	Deleted    bool     `json:"deleted,omitempty"`
	// NeedsUpdate marks a dependency added after the dependent's branch was
	// cut: the dependent's next run first merges the base branch.
	NeedsUpdate bool `json:"-"`
}

// Ref names a linked ticket the way the user sees it: its issue number, or
// its ticket id when it has no issue.
func (l TicketLink) Ref() string {
	if l.Issue != 0 {
		return fmt.Sprintf("#%d", l.Issue)
	}
	return fmt.Sprintf("ticket %d", l.TicketID)
}

// Finished reports whether a dependency no longer holds its dependent back.
func (l TicketLink) Finished() bool { return !l.Deleted && l.Status == StatusDone }

// QuestionKind is how a question is answered.
type QuestionKind string

const (
	QuestionSingle  QuestionKind = "single"
	QuestionMulti   QuestionKind = "multi"
	QuestionText    QuestionKind = "text"
	QuestionConfirm QuestionKind = "confirm"
)

// Preview is an optional artifact shown with an option.
type Preview struct {
	Markdown string `json:"markdown,omitempty"`
	Image    string `json:"image,omitempty"` // artifact id
	URL      string `json:"url,omitempty"`   // live preview link
}

// Option is one answer of a choice question.
type Option struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Recommended bool     `json:"recommended,omitempty"`
	Preview     *Preview `json:"preview,omitempty"`
}

// Condition decides whether a question is visible. Exactly one of the forms is
// set: Question+AnyOf, All, Any or Not.
type Condition struct {
	Question string       `json:"question,omitempty"`
	AnyOf    []string     `json:"anyOf,omitempty"`
	Answered bool         `json:"answered,omitempty"`
	All      []*Condition `json:"all,omitempty"`
	Any      []*Condition `json:"any,omitempty"`
	Not      *Condition   `json:"not,omitempty"`
}

// Question is one node of a question tree.
type Question struct {
	ID         string       `json:"id"`
	Kind       QuestionKind `json:"kind"`
	Title      string       `json:"title"`
	Context    string       `json:"context,omitempty"`
	Options    []Option     `json:"options,omitempty"`
	AllowOther *bool        `json:"allowOther,omitempty"`
	ShowIf     *Condition   `json:"showIf,omitempty"`
}

// OtherAllowed reports whether a free-text answer is accepted.
func (q Question) OtherAllowed() bool {
	if q.AllowOther != nil {
		return *q.AllowOther
	}
	return true
}

// Purpose says what a round's answers feed.
type Purpose string

const (
	PurposeRequirement Purpose = "requirement"
	PurposeDesign      Purpose = "design"
	PurposeFollowUp    Purpose = "follow_up"
	PurposeSpendCap    Purpose = "spend_cap"
	PurposeVerify      Purpose = "verify"
)

// QuestionTree is one round of questions on a ticket.
type QuestionTree struct {
	Purpose   Purpose    `json:"purpose,omitempty"`
	Title     string     `json:"title"`
	Intro     string     `json:"intro,omitempty"`
	Questions []Question `json:"questions"`
}

// Answer is the user's answer to one question.
type Answer struct {
	OptionIDs []string `json:"optionIds,omitempty"`
	Text      string   `json:"text,omitempty"`
}

// Empty reports whether the answer carries nothing.
func (a Answer) Empty() bool { return len(a.OptionIDs) == 0 && a.Text == "" }

// Round is a question tree posted on a ticket plus its answers.
type Round struct {
	TicketID    int64             `json:"ticketId"`
	N           int               `json:"n"`
	Purpose     Purpose           `json:"purpose"`
	Stage       Stage             `json:"stage"` // stage to resume when complete
	Tree        QuestionTree      `json:"tree"`
	Answers     map[string]Answer `json:"answers"`
	CompletedAt *time.Time        `json:"completedAt,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// RunStatus is a run's lifecycle state.
type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunPaused    RunStatus = "paused"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunStopped   RunStatus = "stopped"
	RunLimited   RunStatus = "limited" // ended by a usage limit; will be retried
	RunCapped    RunStatus = "capped"  // ended by a spend cap
)

// Run is one agent process working one stage of a ticket.
type Run struct {
	ID         int64      `json:"id"`
	TicketID   int64      `json:"ticketId"`
	Stage      Stage      `json:"stage"`
	Agent      AgentKind  `json:"agent"`
	Model      string     `json:"model"`
	Effort     string     `json:"effort"`
	AccountID  int64      `json:"accountId,omitempty"`
	SessionID  string     `json:"sessionId,omitempty"`
	Status     RunStatus  `json:"status"`
	Summary    string     `json:"summary,omitempty"`
	Error      string     `json:"error,omitempty"`
	CostUSD    float64    `json:"costUsd"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// EventKind classifies a run log line.
type EventKind string

const (
	EventText       EventKind = "text"
	EventThinking   EventKind = "thinking"
	EventToolCall   EventKind = "tool_call"
	EventToolResult EventKind = "tool_result"
	EventUser       EventKind = "user"   // a message the user sent
	EventSystem     EventKind = "system" // app notes: started, limited, resumed…
	EventError      EventKind = "error"
)

// RunEvent is one line of a run's live log.
type RunEvent struct {
	ID        int64           `json:"id"`
	RunID     int64           `json:"runId"`
	Kind      EventKind       `json:"kind"`
	Text      string          `json:"text"`
	Data      json.RawMessage `json:"data,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Account is a Claude subscription login.
type Account struct {
	ID            int64      `json:"id"`
	Label         string     `json:"label"`
	Priority      int        `json:"priority"`
	ExhaustedTill *time.Time `json:"exhaustedTill,omitempty"`
	Warning       bool       `json:"warning"`
	Utilization   float64    `json:"utilization"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// Schedule is a recurring job on a project.
type Schedule struct {
	ID        int64      `json:"id"`
	ProjectID int64      `json:"projectId"`
	Name      string     `json:"name"`
	Cron      string     `json:"cron"`
	Prompt    string     `json:"prompt"`
	Agent     AgentKind  `json:"agent"`
	Model     string     `json:"model"`
	Effort    string     `json:"effort"`
	Enabled   bool       `json:"enabled"`
	LastRunAt *time.Time `json:"lastRunAt,omitempty"`
	NextRunAt *time.Time `json:"nextRunAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// Artifact is a file a run produced: a screenshot, a report.
type Artifact struct {
	ID        string    `json:"id"`
	TicketID  int64     `json:"ticketId"`
	RunID     int64     `json:"runId"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

// Prototype is one option the user picks from.
type Prototype struct {
	Name        string            `json:"name"`
	Branch      string            `json:"branch"`
	Description string            `json:"description"`
	Artifacts   []string          `json:"artifacts,omitempty"` // screenshot artifact ids
	Preview     *PrototypePreview `json:"preview,omitempty"`
}

// PrototypePreview is how to run a prototype for the live preview.
type PrototypePreview struct {
	Command string `json:"command"`
	Port    int    `json:"port"`
}

// Proof is the test evidence attached to a ticket.
type Proof struct {
	TicketID  int64     `json:"ticketId"`
	RunID     int64     `json:"runId"`
	Verdict   string    `json:"verdict"` // PASS | FAIL
	Report    string    `json:"report"`  // markdown, may reference artifact ids
	Files     []string  `json:"files"`   // artifact ids
	CreatedAt time.Time `json:"createdAt"`
}

// NotifyEvent is a kind of push notification.
type NotifyEvent string

const (
	NotifyQuestions NotifyEvent = "questions_waiting"
	NotifyPRReady   NotifyEvent = "pr_ready"
	NotifyRunFailed NotifyEvent = "run_failed"
	NotifyPaused    NotifyEvent = "run_paused"
	NotifyLimited   NotifyEvent = "usage_limited"
	NotifyJobDone   NotifyEvent = "job_done"
	NotifyMerged    NotifyEvent = "merged"
)

// NotifyEvents lists every notification kind with its default.
var NotifyEvents = map[NotifyEvent]bool{
	NotifyQuestions: true,
	NotifyPRReady:   true,
	NotifyRunFailed: true,
	NotifyPaused:    true,
	NotifyLimited:   false,
	NotifyJobDone:   false,
	NotifyMerged:    false,
}
