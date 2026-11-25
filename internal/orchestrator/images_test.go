package orchestrator

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// Minimal headers http.DetectContentType recognises.
var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 64)...)
	jpegBytes = append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{2}, 64)...)
	gifBytes  = append([]byte("GIF89a"), bytes.Repeat([]byte{3}, 64)...)
	webpBytes = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{4}, 64)...)
)

func (h *harness) upload(b []byte, name string) string {
	h.t.Helper()
	id, err := h.o.UploadImage(h.ctx, h.project.ID, bytes.NewReader(b), name)
	if err != nil {
		h.t.Fatalf("upload %s: %v", name, err)
	}
	return id
}

func (h *harness) addImage(ticketID int64, b []byte, name string) domain.TicketImage {
	h.t.Helper()
	im, err := h.o.AddImage(h.ctx, ticketID, bytes.NewReader(b), name)
	if err != nil {
		h.t.Fatalf("add image %s: %v", name, err)
	}
	return im
}

func (h *harness) imageNumbers(ticketID int64) []int {
	h.t.Helper()
	ims, err := h.st.TicketImages(h.ctx, ticketID)
	if err != nil {
		h.t.Fatal(err)
	}
	var ns []int
	for _, im := range ims {
		ns = append(ns, im.N)
	}
	return ns
}

func (h *harness) issueBody(n int) string {
	h.gh.mu.Lock()
	defer h.gh.mu.Unlock()
	return h.gh.issues[n]
}

func TestTicketImagesReachRunsAndStayOnTheBoard(t *testing.T) {
	h := newHarness(t)
	u1, u2 := h.upload(pngBytes, "shot.png"), h.upload(jpegBytes, "photo.jpeg")
	tk, err := h.o.CreateTicket(h.ctx, h.project.ID, NewTicket{Idea: "Fix the header, see image 2", Agent: domain.AgentFake, Start: true, Images: []string{u1, u2}})
	if err != nil {
		t.Fatal(err)
	}
	if body := h.issueBody(tk.Issue); !strings.Contains(body, "_2 images attached on the board._") || strings.Contains(body, u1) || strings.Contains(body, "/api/") {
		t.Fatalf("issue body = %q, want the image count and no image or link", body)
	}

	r := h.nextRun()
	goal := goalName(h.ticket(tk.ID))
	for _, want := range []string{"## Images", "Open every one", "- image 1: .ship/" + goal + "/images/1.png", "- image 2: .ship/" + goal + "/images/2.jpg"} {
		if !strings.Contains(r.Spec.Prompt, want) {
			t.Fatalf("first run prompt lacks %q:\n%s", want, r.Spec.Prompt)
		}
	}
	imgDir := filepath.Join(r.Info.Workdir, ".ship", goal, "images")
	if got, _ := os.ReadFile(filepath.Join(imgDir, "2.jpg")); !bytes.Equal(got, jpegBytes) {
		t.Fatalf("images/2.jpg = %d bytes, want the uploaded photo", len(got))
	}
	if out := mustGit(t, r.Info.Workdir, "status", "--porcelain", "--ignored=no"); out != "" {
		t.Fatalf("images show up in git status:\n%s", out)
	}

	// Mid-run changes do not reach the running run.
	if im := h.addImage(tk.ID, gifBytes, "sketch.gif"); im.N != 3 {
		t.Fatalf("third image numbered %d", im.N)
	}
	if _, err := os.Stat(filepath.Join(imgDir, "3.gif")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("image added mid-run reached the run's goal dir: %v", err)
	}
	if body := h.issueBody(tk.Issue); !strings.Contains(body, "_3 images attached on the board._") {
		t.Fatalf("issue body after add = %q", body)
	}
	if err := h.o.RemoveImage(h.ctx, tk.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := h.o.RemoveImage(h.ctx, tk.ID, 2); err == nil {
		t.Fatal("removing image 2 twice succeeded")
	}
	if im := h.addImage(tk.ID, webpBytes, "x.webp"); im.N != 4 {
		t.Fatalf("image after a removal numbered %d, want 4 (numbers are never reused)", im.N)
	}
	if got := h.imageNumbers(tk.ID); !slices.Equal(got, []int{1, 3, 4}) {
		t.Fatalf("images = %v, want [1 3 4]", got)
	}
	if body := h.issueBody(tk.Issue); !strings.Contains(body, "_3 images attached on the board._") {
		t.Fatalf("issue body after remove+add = %q", body)
	}

	// The next run's goal dir holds exactly the current images; a symlink
	// planted where the images go is replaced, not followed.
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(imgDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, imgDir); err != nil {
		t.Fatal(err)
	}
	tc, err := h.o.ticketContext(h.ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeGoalDir(r.Info.Workdir, tc, h.o.ArtifactPath); err != nil {
		t.Fatal(err)
	}
	var names []string
	ents, _ := os.ReadDir(imgDir)
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"1.png", "3.gif", "4.webp"}) {
		t.Fatalf("next run's images = %v", names)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatal("images written through a planted symlink")
	}

	// Deleting the ticket deletes the image files.
	paths := []string{}
	for _, im := range tc.Images {
		paths = append(paths, h.o.ArtifactPath(im.ArtifactID))
	}
	if err := h.o.Stop(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	h.waitInactive(tk.ID)
	if err := h.o.DeleteTicket(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("image file %s survived the ticket: %v", p, err)
		}
	}
}

func TestImageLimitsAndTypes(t *testing.T) {
	h := newHarness(t, withoutRun())
	for _, c := range []struct {
		name string
		b    []byte
	}{
		{"doc.pdf", []byte("%PDF-1.7\n1 0 obj")},
		{"notes.txt", []byte("just some text")},
		{"evil.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"empty.png", nil},
		{"photo.heic", []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00")},
	} {
		_, err := h.o.UploadImage(h.ctx, h.project.ID, bytes.NewReader(c.b), c.name)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "only images") {
			t.Errorf("%s: err = %v, want the only-images error", c.name, err)
		}
	}
	for _, b := range [][]byte{pngBytes, jpegBytes, gifBytes, webpBytes} {
		h.upload(b, "ok")
	}

	big := append(append([]byte{}, pngBytes...), make([]byte, domain.MaxImageSize)...)
	if _, err := h.o.UploadImage(h.ctx, h.project.ID, bytes.NewReader(big), "big.png"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "20 MB") {
		t.Fatalf("oversized image: err = %v, want the 20 MB error", err)
	}

	tk := h.newTicket(domain.Ticket{Status: domain.StatusBacklog})
	for range domain.MaxTicketImages {
		h.addImage(tk.ID, pngBytes, "a.png")
	}
	if _, err := h.o.AddImage(h.ctx, tk.ID, bytes.NewReader(pngBytes), "11.png"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "at most 10 images") {
		t.Fatalf("11th image: err = %v", err)
	}
	if got := len(h.imageNumbers(tk.ID)); got != domain.MaxTicketImages {
		t.Fatalf("images after the refused 11th = %d", got)
	}
	ents, _ := os.ReadDir(h.cfg.Path("artifacts"))
	if want := 4 + domain.MaxTicketImages; len(ents) != want {
		t.Fatalf("artifact files = %d, want %d (refused uploads leave nothing behind)", len(ents), want)
	}

	var ids []string
	for range domain.MaxTicketImages + 1 {
		ids = append(ids, h.upload(pngBytes, "x.png"))
	}
	if _, err := h.o.CreateTicket(h.ctx, h.project.ID, NewTicket{Idea: "x", Agent: domain.AgentFake, Images: ids}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create with 11 images: err = %v", err)
	}
	before, _ := h.st.ProjectTickets(h.ctx, h.project.ID)
	if _, err := h.o.CreateTicket(h.ctx, h.project.ID, NewTicket{Idea: "x", Agent: domain.AgentFake, Images: []string{ids[0], "nope"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create with an unknown upload: err = %v", err)
	}
	after, _ := h.st.ProjectTickets(h.ctx, h.project.ID)
	if len(after) != len(before) {
		t.Fatal("a ticket was created although one of its images was missing")
	}
	// The failed create rolled back, so the upload is still attachable.
	tk2, err := h.o.CreateTicket(h.ctx, h.project.ID, NewTicket{Idea: "x", Agent: domain.AgentFake, Images: []string{ids[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.imageNumbers(tk2.ID); !slices.Equal(got, []int{1}) {
		t.Fatalf("images = %v", got)
	}
}

func TestStaleUploadsAreDeleted(t *testing.T) {
	h := newHarness(t, withoutRun())
	old := h.upload(pngBytes, "old.png")
	h.clock.Advance(uploadTTL + time.Minute)
	fresh := h.upload(pngBytes, "fresh.png")
	if err := h.o.cleanUploads(h.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.o.ArtifactPath(old)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale upload kept: %v", err)
	}
	if _, err := os.Stat(h.o.ArtifactPath(fresh)); err != nil {
		t.Fatalf("fresh upload removed: %v", err)
	}
}

func TestBlindModelIsToldImagesExist(t *testing.T) {
	h := newHarness(t)
	h.o.SetPiImageModels(map[string]bool{"anthropic/claude-sonnet-latest": true})
	if !h.o.SeesImages(domain.AgentClaude, "haiku") || !h.o.SeesImages(domain.AgentPi, "~anthropic/claude-sonnet-latest") || h.o.SeesImages(domain.AgentPi, "google/gemini-3-pro") {
		t.Fatal("SeesImages disagrees with the catalog")
	}
	tk := h.newTicket(domain.Ticket{Agent: domain.AgentPi, Model: "google/gemini-3-pro", Status: domain.StatusBacklog})
	h.addImage(tk.ID, pngBytes, "a.png")
	if _, err := h.o.StartTicket(h.ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	r := h.nextRun()
	if !strings.Contains(r.Spec.Prompt, "cannot look at images") || !strings.Contains(r.Spec.Prompt, "- image 1: ") {
		t.Fatalf("blind run prompt:\n%s", r.Spec.Prompt)
	}
}
