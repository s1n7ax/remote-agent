package orchestrator

import (
	"strings"
	"testing"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

func TestLinkProofImages(t *testing.T) {
	stored := map[string]string{"/w/.ship/g/verify/1-after.png": "a1", "/w/b.png": "b2"}
	resolve := func(p string) string {
		if strings.HasPrefix(p, "/") {
			return p
		}
		return "/w/" + p
	}
	in := "![s](.ship/g/verify/1-after.png) ![t](b.png \"title\") ![x](other.png) [link](b.png) ![h](https://x/b.png) <img src=\"b.png\" alt=\"y\"> ![abs](/w/b.png)"
	want := "![s](/api/artifacts/a1) ![t](/api/artifacts/b2 \"title\") ![x](other.png) [link](b.png) ![h](https://x/b.png) <img src=\"/api/artifacts/b2\" alt=\"y\"> ![abs](/api/artifacts/b2)"
	if got := linkProofImages(in, stored, resolve); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestResolveProofImages(t *testing.T) {
	arts := []domain.Artifact{
		{ID: "a1", Name: "1-after.png"},
		{ID: "b1", Name: "dup.png"},
		{ID: "b2", Name: "dup.png"},
	}
	in := "| ![screenshot](.ship/g/verify/3/1-after.png) | ![d](dup.png) | ![n](none.png) | ![k](/api/artifacts/zz) | ![u](https://h/1-after.png) |"
	want := "| ![screenshot](/api/artifacts/a1) | ![d](dup.png) | ![n](none.png) | ![k](/api/artifacts/zz) | ![u](https://h/1-after.png) |"
	if got := ResolveProofImages(in, arts); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := ResolveProofImages("no images", nil); got != "no images" {
		t.Fatalf("text changed: %q", got)
	}
}

func TestProofComment(t *testing.T) {
	report := "# V\n\n| ![screenshot](.ship/g/verify/1-before.png) | ![screenshot](/api/artifacts/a1) |\n\n<img src=\"x/y.png\">\n\nSee [the log](log.txt)."
	got := proofComment("<!-- m -->", "PASS", report, "https://app/#/p/1/t/2/proof")
	for _, bad := range []string{"![", "<img", ".ship/", "/api/artifacts/"} {
		if strings.Contains(got, bad) {
			t.Errorf("comment contains %q:\n%s", bad, got)
		}
	}
	for _, want := range []string{"<!-- m -->\n## Verification: PASS", "_screenshot: 1-before.png_", "_screenshot: a1_", "_screenshot: y.png_",
		"[the log](log.txt)", "[View screenshots in remote-agent (tailnet only)](https://app/#/p/1/t/2/proof)"} {
		if !strings.Contains(got, want) {
			t.Errorf("comment lacks %q:\n%s", want, got)
		}
	}
}
