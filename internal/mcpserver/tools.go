package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/questions"
)

// Tool names.
const (
	ToolSubmitQuestions   = "submit_questions"
	ToolSubmitRequirement = "submit_requirement"
	ToolSubmitDecisions   = "submit_decisions"
	ToolSubmitPrototypes  = "submit_prototypes"
	ToolStartPreview      = "start_preview"
	ToolSubmitProof       = "submit_proof"
	ToolUploadArtifact    = "upload_artifact"
	ToolPushBranch        = "push_branch"
	ToolOpenPR            = "open_pr"
	ToolUpdatePR          = "update_pr"
	ToolCIStatus          = "ci_status"
	ToolCILogs            = "ci_logs"
	ToolGetIssue          = "get_issue"
	ToolComment           = "comment"
	ToolListPulls         = "list_pulls"
	ToolStageDone         = "stage_done"
)

// ToolNames lists every tool. There is no merge tool.
var ToolNames = []string{
	ToolSubmitQuestions, ToolSubmitRequirement, ToolSubmitDecisions, ToolSubmitPrototypes,
	ToolStartPreview, ToolSubmitProof, ToolUploadArtifact, ToolPushBranch, ToolOpenPR,
	ToolUpdatePR, ToolCIStatus, ToolCILogs, ToolGetIssue, ToolComment, ToolListPulls,
	ToolStageDone,
}

// tools binds the tool handlers to one run.
type tools struct {
	host Host
	run  Run
}

// toolDef is one tool ready to add to a server.
type toolDef struct {
	tool *mcp.Tool
	add  func(*mcp.Server)
}

// define builds a tool whose input decodes into In and whose result is v
// rendered as text (strings as is, anything else as JSON).
func define[In any](name, description, schema string, readOnly bool, h func(context.Context, In) (any, error)) toolDef {
	tool := &mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: json.RawMessage(schema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}
	return toolDef{tool: tool, add: func(s *mcp.Server) {
		mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			v, err := h(ctx, in)
			if err != nil {
				return nil, nil, err
			}
			text, ok := v.(string)
			if !ok {
				b, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					return nil, nil, fmt.Errorf("encode result: %w", err)
				}
				text = string(b)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
		})
	}}
}

type (
	questionsIn struct {
		Purpose domain.Purpose      `json:"purpose"`
		Tree    domain.QuestionTree `json:"tree"`
	}
	markdownIn struct {
		Markdown string `json:"markdown"`
	}
	prototypesIn struct {
		Prototypes []domain.Prototype `json:"prototypes"`
	}
	previewIn struct {
		Branch  string `json:"branch"`
		Command string `json:"command"`
		Port    int    `json:"port"`
	}
	proofIn struct {
		Verdict string   `json:"verdict"`
		Report  string   `json:"report_markdown"`
		Files   []string `json:"files"`
	}
	pathIn struct {
		Path string `json:"path"`
	}
	openPRIn struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Draft *bool  `json:"draft"`
	}
	bodyIn struct {
		Body string `json:"body"`
	}
	checkIn struct {
		Check string `json:"check"`
	}
	summaryIn struct {
		Summary string `json:"summary"`
	}
	noIn struct{}
)

// agentPurposes are the purposes an agent may post; spend_cap rounds are
// the app's own.
var agentPurposes = []domain.Purpose{
	domain.PurposeRequirement, domain.PurposeDesign, domain.PurposeFollowUp, domain.PurposeVerify,
}

func required(field, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

func (t *tools) defs() []toolDef {
	return []toolDef{
		define(ToolSubmitQuestions, descSubmitQuestions, schemaSubmitQuestions, false, t.submitQuestions),
		define(ToolSubmitRequirement, descSubmitRequirement, schemaMarkdown, false, func(ctx context.Context, in markdownIn) (any, error) {
			if err := required("markdown", in.Markdown); err != nil {
				return nil, err
			}
			return "requirement saved", t.host.SubmitRequirement(ctx, t.run, in.Markdown)
		}),
		define(ToolSubmitDecisions, descSubmitDecisions, schemaMarkdown, false, func(ctx context.Context, in markdownIn) (any, error) {
			if err := required("markdown", in.Markdown); err != nil {
				return nil, err
			}
			return "decisions saved", t.host.SubmitDecisions(ctx, t.run, in.Markdown)
		}),
		define(ToolSubmitPrototypes, descSubmitPrototypes, schemaSubmitPrototypes, false, t.submitPrototypes),
		define(ToolStartPreview, descStartPreview, schemaStartPreview, false, func(ctx context.Context, in previewIn) (any, error) {
			if err := required("command", in.Command); err != nil {
				return nil, err
			}
			if in.Port < 1 || in.Port > 65535 {
				return nil, fmt.Errorf("port %d is not a TCP port", in.Port)
			}
			url, err := t.host.StartPreview(ctx, t.run, Preview(in))
			if err != nil {
				return nil, err
			}
			return map[string]string{"url": url}, nil
		}),
		define(ToolSubmitProof, descSubmitProof, schemaSubmitProof, false, t.submitProof),
		define(ToolUploadArtifact, descUploadArtifact, schemaPath, false, func(ctx context.Context, in pathIn) (any, error) {
			path, err := resolveFile(t.run.Workdir, in.Path)
			if err != nil {
				return nil, err
			}
			a, err := t.host.UploadArtifact(ctx, t.run, path)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ID, "name": a.Name, "mime": a.MIME, "size": a.Size}, nil
		}),
		define(ToolPushBranch, descPushBranch, schemaNone, false, func(ctx context.Context, _ noIn) (any, error) {
			return t.host.PushBranch(ctx, t.run)
		}),
		define(ToolOpenPR, descOpenPR, schemaOpenPR, false, func(ctx context.Context, in openPRIn) (any, error) {
			if err := errors.Join(required("title", in.Title), required("body", in.Body)); err != nil {
				return nil, err
			}
			draft := in.Draft == nil || *in.Draft
			return t.host.OpenPR(ctx, t.run, NewPR{Title: in.Title, Body: in.Body, Draft: draft})
		}),
		define(ToolUpdatePR, descUpdatePR, schemaBody, false, func(ctx context.Context, in bodyIn) (any, error) {
			if err := required("body", in.Body); err != nil {
				return nil, err
			}
			return t.host.UpdatePR(ctx, t.run, in.Body)
		}),
		define(ToolCIStatus, descCIStatus, schemaNone, true, func(ctx context.Context, _ noIn) (any, error) {
			return t.host.CIStatus(ctx, t.run)
		}),
		define(ToolCILogs, descCILogs, schemaCILogs, true, func(ctx context.Context, in checkIn) (any, error) {
			if err := required("check", in.Check); err != nil {
				return nil, err
			}
			return t.host.CILogs(ctx, t.run, in.Check)
		}),
		define(ToolGetIssue, descGetIssue, schemaNone, true, func(ctx context.Context, _ noIn) (any, error) {
			return t.host.GetIssue(ctx, t.run)
		}),
		define(ToolComment, descComment, schemaBody, false, func(ctx context.Context, in bodyIn) (any, error) {
			if err := required("body", in.Body); err != nil {
				return nil, err
			}
			return t.host.Comment(ctx, t.run, in.Body)
		}),
		define(ToolListPulls, descListPulls, schemaNone, true, func(ctx context.Context, _ noIn) (any, error) {
			return t.host.ListPulls(ctx, t.run)
		}),
		define(ToolStageDone, descStageDone, schemaStageDone, false, func(ctx context.Context, in summaryIn) (any, error) {
			if err := required("summary", in.Summary); err != nil {
				return nil, err
			}
			return "stage done; end your turn now", t.host.StageDone(ctx, t.run, in.Summary)
		}),
	}
}

func (t *tools) submitQuestions(ctx context.Context, in questionsIn) (any, error) {
	if in.Purpose == "" {
		in.Purpose = domain.PurposeFollowUp
	}
	ok := false
	for _, p := range agentPurposes {
		ok = ok || p == in.Purpose
	}
	if !ok {
		return nil, fmt.Errorf("purpose %q is not one of requirement, design, follow_up, verify", in.Purpose)
	}
	if err := questions.Validate(in.Tree); err != nil {
		return nil, fmt.Errorf("fix the question tree and call submit_questions again: %w", err)
	}
	round, err := t.host.SubmitQuestions(ctx, t.run, in.Purpose, in.Tree)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"round":   round,
		"message": "Questions posted. The user answers asynchronously; call stage_done now and end your turn. A later run continues with the answers.",
	}, nil
}

func (t *tools) submitPrototypes(ctx context.Context, in prototypesIn) (any, error) {
	if len(in.Prototypes) == 0 {
		return nil, errors.New("at least one prototype is required")
	}
	var errs []error
	names := map[string]bool{}
	for i, p := range in.Prototypes {
		if strings.TrimSpace(p.Name) == "" {
			errs = append(errs, fmt.Errorf("prototypes[%d]: name is required", i))
		} else if names[p.Name] {
			errs = append(errs, fmt.Errorf("prototypes[%d]: duplicate name %q", i, p.Name))
		}
		names[p.Name] = true
		if strings.TrimSpace(p.Branch) == "" {
			errs = append(errs, fmt.Errorf("prototypes[%d]: branch is required", i))
		}
		if strings.TrimSpace(p.Summary) == "" {
			errs = append(errs, fmt.Errorf("prototypes[%d]: summary is required", i))
		}
		if p.PreviewPort < 0 || p.PreviewPort > 65535 || (p.PreviewCmd != "") != (p.PreviewPort != 0) {
			errs = append(errs, fmt.Errorf("prototypes[%d]: previewCmd and previewPort go together", i))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return "prototypes saved", t.host.SubmitPrototypes(ctx, t.run, in.Prototypes)
}

func (t *tools) submitProof(ctx context.Context, in proofIn) (any, error) {
	if in.Verdict != "PASS" && in.Verdict != "FAIL" {
		return nil, fmt.Errorf("verdict must be PASS or FAIL, got %q", in.Verdict)
	}
	if err := required("report_markdown", in.Report); err != nil {
		return nil, err
	}
	files := make([]string, 0, len(in.Files))
	for _, f := range in.Files {
		path, err := resolveFile(t.run.Workdir, f)
		if err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	return "proof attached", t.host.SubmitProof(ctx, t.run, Proof{Verdict: in.Verdict, Report: in.Report, Files: files})
}
