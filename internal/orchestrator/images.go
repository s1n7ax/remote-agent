package orchestrator

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/s1n7ax/remote-agent/internal/domain"
	"github.com/s1n7ax/remote-agent/internal/store"
)

// imageExt maps the image types a ticket accepts to their file extension.
// The type is sniffed from the bytes, never taken from the name or the
// client, so nothing stored here can be served as HTML or SVG.
var imageExt = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// uploadTTL is how long an image uploaded for a ticket that was never
// created is kept.
const uploadTTL = 24 * time.Hour

var (
	errNotImage  = invalid("only images (JPEG, PNG, WebP, GIF) can be attached")
	errImageSize = invalid("each image can be at most %d MB", domain.MaxImageSize>>20)
)

// storeImage writes an uploaded image into the artifact store and returns
// its metadata (no row is written). It refuses anything that is not a
// JPEG, PNG, GIF or WebP, or is larger than domain.MaxImageSize.
func (o *Orchestrator) storeImage(r io.Reader, name string) (domain.Artifact, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return domain.Artifact{}, uploadErr(err)
	}
	head = head[:n]
	mt := http.DetectContentType(head)
	if imageExt[mt] == "" {
		return domain.Artifact{}, errNotImage
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	if err := os.MkdirAll(o.cfg.Path("artifacts"), 0o700); err != nil {
		return domain.Artifact{}, fmt.Errorf("store image: %w", err)
	}
	path := o.ArtifactPath(id)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return domain.Artifact{}, fmt.Errorf("store image: %w", err)
	}
	size, err := io.Copy(out, io.LimitReader(io.MultiReader(bytes.NewReader(head), r), domain.MaxImageSize+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && size > domain.MaxImageSize {
		err = errImageSize
	}
	if err != nil {
		_ = os.Remove(path)
		return domain.Artifact{}, uploadErr(err)
	}
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." || name == "/" {
		name = "image." + imageExt[mt]
	}
	return domain.Artifact{ID: id, Name: name, MIME: mt, Size: size}, nil
}

// uploadErr turns a body that hit the server's size cap into the size
// error, so the client sees the limit and not a transport error.
func uploadErr(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return errImageSize
	}
	return err
}

// UploadImage stores an image for a ticket that is about to be created in
// project projectID; CreateTicket attaches it by id.
func (o *Orchestrator) UploadImage(ctx context.Context, projectID int64, r io.Reader, name string) (string, error) {
	if _, err := o.st.Project(ctx, projectID); err != nil {
		return "", err
	}
	a, err := o.storeImage(r, name)
	if err != nil {
		return "", err
	}
	if err := o.st.AddUpload(ctx, store.Upload{ID: a.ID, ProjectID: projectID, Name: a.Name, MIME: a.MIME, Size: a.Size, CreatedAt: o.now()}); err != nil {
		_ = os.Remove(o.ArtifactPath(a.ID))
		return "", err
	}
	return a.ID, nil
}

// AddImage attaches an image to a ticket as its next number. A run that is
// already working sees it from the next stage's run on.
func (o *Orchestrator) AddImage(ctx context.Context, ticketID int64, r io.Reader, name string) (domain.TicketImage, error) {
	t, err := o.st.Ticket(ctx, ticketID)
	if err != nil {
		return domain.TicketImage{}, err
	}
	if t.Kind == domain.KindJob {
		return domain.TicketImage{}, invalid("jobs cannot have images")
	}
	a, err := o.storeImage(r, name)
	if err != nil {
		return domain.TicketImage{}, err
	}
	a.TicketID = t.ID
	im, err := o.st.AddTicketImage(ctx, a)
	if err != nil {
		_ = os.Remove(o.ArtifactPath(a.ID))
		if errors.Is(err, store.ErrTooManyImages) {
			return im, invalid("%v", err)
		}
		return im, err
	}
	o.syncIssue(ctx, t)
	return im, nil
}

// RemoveImage removes image n from a ticket. Its number is not reused.
func (o *Orchestrator) RemoveImage(ctx context.Context, ticketID int64, n int) error {
	t, err := o.st.Ticket(ctx, ticketID)
	if err != nil {
		return err
	}
	id, err := o.st.RemoveTicketImage(ctx, ticketID, n)
	if err != nil {
		return err
	}
	o.RemoveArtifactFiles(id)
	o.syncIssue(ctx, t)
	return nil
}

// removeImageFiles deletes the files of a ticket's images (before the
// ticket's rows go).
func (o *Orchestrator) removeImageFiles(ctx context.Context, ticketID int64) {
	ims, err := o.st.TicketImages(ctx, ticketID)
	if err != nil {
		o.log.Warn("list images to remove", "ticket", ticketID, "err", err)
		return
	}
	for _, im := range ims {
		o.RemoveArtifactFiles(im.ArtifactID)
	}
}

// RemoveArtifactFiles deletes the files of artifacts (or uploads) whose rows
// are gone or about to go; a file already missing is fine.
func (o *Orchestrator) RemoveArtifactFiles(ids ...string) {
	for _, id := range ids {
		if err := os.Remove(o.ArtifactPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			o.log.Warn("remove artifact file", "id", id, "err", err)
		}
	}
}

// cleanUploads deletes uploads that were never attached to a ticket, at
// most once an hour.
func (o *Orchestrator) cleanUploads(ctx context.Context) error {
	o.mu.Lock()
	due := o.now().Sub(o.lastUploadSweep) >= time.Hour
	if due {
		o.lastUploadSweep = o.now()
	}
	o.mu.Unlock()
	if !due {
		return nil
	}
	ids, err := o.st.DeleteUploadsBefore(ctx, o.now().Add(-uploadTTL))
	o.RemoveArtifactFiles(ids...)
	return err
}

// SetPiImageModels records which pi models can look at images (from pi's
// model catalog).
func (o *Orchestrator) SetPiImageModels(ms map[string]bool) {
	o.mu.Lock()
	o.piImages = ms
	o.mu.Unlock()
}

// PiImageModels lists the pi models known to look at images.
func (o *Orchestrator) PiImageModels() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.piImages))
	for m := range o.piImages {
		out = append(out, m)
	}
	return out
}

// SeesImages reports whether an agent running model can look at images.
// Every Claude model can; a pi model counts only when pi's catalog says
// so, so an unknown one gets the warning rather than a silent blind run.
func (o *Orchestrator) SeesImages(kind domain.AgentKind, model string) bool {
	if kind != domain.AgentPi {
		return true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.piImages[strings.TrimPrefix(model, "~")]
}

// issueBody is the ticket's GitHub issue body: the idea, or once written
// the requirement with the idea quoted under it, and a footer saying how
// many images the ticket has on the board. Images never go to GitHub.
func issueBody(t domain.Ticket, images int) string {
	var b strings.Builder
	if t.Requirement != "" {
		fmt.Fprintf(&b, "%s\n\n---\n_Original idea:_\n\n%s\n\n", t.Requirement, quote(t.Idea))
	} else {
		fmt.Fprintf(&b, "%s\n\n---\n", t.Idea)
	}
	switch {
	case images == 1:
		b.WriteString("_1 image attached on the board._\n\n")
	case images > 1:
		fmt.Fprintf(&b, "_%d images attached on the board._\n\n", images)
	}
	fmt.Fprintf(&b, "_Tracked by remote-agent (ticket %d)._", t.ID)
	return b.String()
}

// syncIssue rewrites the ticket's issue body, best effort.
func (o *Orchestrator) syncIssue(ctx context.Context, t domain.Ticket) {
	if t.Issue == 0 {
		return
	}
	p, err := o.st.Project(ctx, t.ProjectID)
	if err != nil {
		o.log.Warn("update issue", "ticket", t.ID, "err", err)
		return
	}
	ims, err := o.st.TicketImages(ctx, t.ID)
	if err != nil {
		o.log.Warn("update issue", "ticket", t.ID, "err", err)
		return
	}
	if err := o.gh.UpdateIssueBody(ctx, p.Repo, t.Issue, issueBody(t, len(ims))); err != nil {
		o.log.Warn("update issue", "ticket", t.ID, "err", err)
	}
}
