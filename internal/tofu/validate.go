package tofu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Result is the outcome of running the real tool over generated configuration.
type Result struct {
	OK bool `json:"ok"`
	// Tool is the binary that ran, empty when none was found.
	Tool   string `json:"tool"`
	Output string `json:"output"`
}

// Validate writes configuration to a temporary directory and runs the real
// `init` and `validate` against it.
//
// The configuration is generated here rather than accepted from the caller: this
// hands a file to a tool that executes provider plugins, so it must be something
// this process produced.
func Validate(ctx context.Context, hcl string) Result {
	bin, err := findTool()
	if err != nil {
		return Result{Output: err.Error()}
	}

	// One working directory, reused. A fresh temporary directory each time means
	// a full `init` on every press — around 18 seconds — where a warm one is a
	// couple. The mutex is what makes reuse safe: two presses must not share a
	// directory mid-write.
	validating.Lock()
	defer validating.Unlock()

	dir, err := workDir()
	if err != nil {
		return Result{Tool: filepath.Base(bin), Output: err.Error()}
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o600); err != nil {
		return Result{Tool: filepath.Base(bin), Output: err.Error()}
	}

	env := append(os.Environ(),
		// Providers are large and identical between runs, so cache them: without
		// this every validation re-downloads several hundred megabytes.
		"TF_PLUGIN_CACHE_DIR="+pluginCache(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
	)

	for _, step := range [][]string{
		{"init", "-no-color", "-input=false"},
		{"validate", "-no-color"},
	} {
		cmd := exec.CommandContext(ctx, bin, step...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			text := strings.TrimSpace(string(out))
			if text == "" {
				text = err.Error()
			}
			if ctx.Err() != nil {
				text = "Timed out running " + step[0] + ". The first run downloads providers and can be slow.\n\n" + text
			}
			return Result{Tool: filepath.Base(bin), Output: text}
		}
		// `init` output is noise once it succeeds; only `validate` is reported.
		if step[0] == "validate" {
			return Result{OK: true, Tool: filepath.Base(bin), Output: strings.TrimSpace(string(out))}
		}
	}
	return Result{Tool: filepath.Base(bin), Output: "no validate step ran"}
}

// findTool prefers OpenTofu, since that is what the generated configuration
// targets, and falls back to Terraform.
func findTool() (string, error) {
	for _, candidate := range []string{"tofu", "terraform"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("neither `tofu` nor `terraform` is on PATH — install one to validate from here")
}

// validating serialises runs: they share a working directory, and `init` writing
// a lock file while another run reads it would fail confusingly.
var validating sync.Mutex

// workDir is the reused working directory. Keeping .terraform between runs is
// what makes a second validation fast.
func workDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "infrachart", "validate")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func pluginCache() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "infrachart", "plugin-cache")
	// A missing cache directory makes the tool fail rather than skip the cache.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.TempDir()
	}
	return dir
}
