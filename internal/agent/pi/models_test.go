package pi

import (
	"reflect"
	"testing"
)

func TestParseImageModels(t *testing.T) {
	out := []byte(`Warning: something first
provider    model                               context  max-out  thinking  images
openrouter  ~anthropic/claude-sonnet-latest     1M       128K     yes       yes
openrouter  ~deepseek/deepseek-pro-latest       1.0M     393.2K   yes       no
openrouter  google/gemini-3-pro-image           65.5K    32.8K    yes       yes
other       some/model                          1M       1M       no        yes
`)
	got := ParseImageModels(out, "openrouter")
	want := map[string]bool{"anthropic/claude-sonnet-latest": true, "google/gemini-3-pro-image": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := ParseImageModels([]byte("no header here\nopenrouter a/b yes\n"), "openrouter"); len(got) != 0 {
		t.Fatalf("without a header nothing is parsed, got %v", got)
	}
}
