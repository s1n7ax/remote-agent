package orchestrator

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// artifactURLPrefix is how a proof report points at a stored artifact. The
// web app turns these into image URLs (artifactUrl) and treats every other
// image reference as unresolved.
const artifactURLPrefix = "/api/artifacts/"

// proofImageRe matches a markdown image, ![alt](url "title"), or the src of
// an HTML <img>. Groups: 1 alt, 2 url, 3 title (markdown); 4 prefix, 5 src,
// 6 rest of the tag (HTML).
var proofImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(\s*<?([^)\s>]+)>?((?:\s+"[^"]*")?)\s*\)|(<img\b[^>]*?\bsrc=["'])([^"']*)(["'][^>]*>)`)

// imageRef is one image reference found in a report.
type imageRef struct {
	url  string
	html bool
	m    []string
}

// withURL returns the reference rewritten to point at u.
func (r imageRef) withURL(u string) string {
	if r.html {
		return r.m[4] + u + r.m[6]
	}
	return "![" + r.m[1] + "](" + u + r.m[3] + ")"
}

// mapImages replaces every image reference in report with what fn returns.
func mapImages(report string, fn func(imageRef) string) string {
	return proofImageRe.ReplaceAllStringFunc(report, func(s string) string {
		m := proofImageRe.FindStringSubmatch(s)
		if m[4] != "" {
			return fn(imageRef{url: m[5], html: true, m: m})
		}
		return fn(imageRef{url: m[2], m: m})
	})
}

// relativeRef reports whether an image URL is a relative path (a checkout
// path), not an absolute URL or an app path.
func relativeRef(u string) bool {
	p, err := url.Parse(u)
	return err == nil && p.Scheme == "" && p.Host == "" && p.Path != "" && !strings.HasPrefix(p.Path, "/")
}

// linkProofImages points every image whose path, resolved against workdir,
// is one of the stored files at that file's artifact. stored maps a file's
// resolved path to its artifact id; resolve turns a report path into one.
func linkProofImages(report string, stored map[string]string, resolve func(string) string) string {
	return mapImages(report, func(r imageRef) string {
		if relativeRef(r.url) || filepath.IsAbs(r.url) {
			if id, ok := stored[resolve(r.url)]; ok {
				return r.withURL(artifactURLPrefix + id)
			}
		}
		return r.m[0]
	})
}

// ResolveProofImages points every image still referenced by a relative path
// (proofs stored before links were rewritten at submit) at the proof's
// artifact with the same file name. A name no artifact has, or several do,
// stays unresolved and the app shows a placeholder for it.
func ResolveProofImages(report string, artifacts []domain.Artifact) string {
	byName := map[string][]string{}
	for _, a := range artifacts {
		byName[a.Name] = append(byName[a.Name], a.ID)
	}
	return mapImages(report, func(r imageRef) string {
		if !relativeRef(r.url) {
			return r.m[0]
		}
		if ids := byName[path.Base(r.url)]; len(ids) == 1 {
			return r.withURL(artifactURLPrefix + ids[0])
		}
		return r.m[0]
	})
}

// proofComment is the PR comment for a proof: the report as text, every
// image replaced by its file name, and a link to the ticket's Proof tab.
// Nothing in it points at the app or the checkout as an image.
func proofComment(marker, verdict, report, proofURL string) string {
	text := mapImages(report, func(r imageRef) string {
		name := path.Base(r.url)
		if name == "." || name == "/" || strings.HasPrefix(r.url, "data:") {
			return "_screenshot_"
		}
		return "_screenshot: " + name + "_"
	})
	return fmt.Sprintf("%s\n## Verification: %s\n\n%s\n\n[View screenshots in remote-agent (tailnet only)](%s)", marker, verdict, text, proofURL)
}
