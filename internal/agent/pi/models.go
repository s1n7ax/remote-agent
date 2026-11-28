package pi

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ImageModels asks pi's built-in model catalog (`pi --list-models`) which
// models of the runner's provider can look at images. dir is an empty,
// writable directory used as pi's home and agent dir; wrap, when set, runs
// pi in a sandbox. The catalog is offline, so the sandbox may (and should)
// have no network. pi lists nothing without a key for the provider, so it is
// given a placeholder one.
func (r Runner) ImageModels(ctx context.Context, dir string, wrap func(argv, env []string) ([]string, []string)) (map[string]bool, error) {
	bin := r.Bin
	if bin == "" {
		bin = "pi"
	}
	provider := r.Provider
	if provider == "" {
		provider = "openrouter"
	}
	argv := []string{bin, "--list-models"}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "PI_CODING_AGENT_DIR=" + dir,
		strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY=unused"}
	if wrap != nil {
		argv, env = wrap(argv, env)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pi --list-models: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return ParseImageModels(out, provider), nil
}

// ParseImageModels reads `pi --list-models` output: a header row naming the
// columns (provider, model, …, images) and one whitespace-separated row per
// model. It returns the provider's models whose images column is "yes".
// pi marks moving aliases with a leading "~"; they are stored without it,
// the way model ids are passed to --model.
func ParseImageModels(out []byte, provider string) map[string]bool {
	ms := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	col := -1
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if col < 0 {
			for i, name := range f {
				if name == "images" {
					col = i
				}
			}
			if col >= 0 && (len(f) < 2 || f[0] != "provider" || f[1] != "model") {
				col = -1
			}
			continue
		}
		if len(f) <= col || f[0] != provider {
			continue
		}
		if f[col] == "yes" {
			ms[strings.TrimPrefix(f[1], "~")] = true
		}
	}
	return ms
}
