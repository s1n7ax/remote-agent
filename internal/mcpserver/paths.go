package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveFile turns path (absolute, or relative to workdir) into an absolute
// path of a regular file that lies inside workdir after resolving symlinks.
func resolveFile(workdir, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	root, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return "", fmt.Errorf("workdir: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("file %q: %w", path, err)
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file %q is outside the working directory %s", path, workdir)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", path)
	}
	return real, nil
}
