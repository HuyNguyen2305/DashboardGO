// Command fmthook is the Claude Code PostToolUse hook for Edit and Write.
// It formats and lints the edited file. Exit codes follow the hook protocol:
// 0 = clean, 2 = issues remain (stderr is shown to Claude to fix),
// 1 = the hook itself could not run (stderr is shown to the user only).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dashboardgo/internal/devtools/fmthook"
)

type hookInput struct {
	Cwd       string `json:"cwd"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

func main() {
	os.Exit(run(os.Stdin, os.Stderr))
}

func run(stdin io.Reader, stderr io.Writer) int {
	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		fmt.Fprintln(stderr, "fmthook: read hook input:", err)
		return 1
	}
	file := in.ToolInput.FilePath
	if file == "" {
		return 0
	}
	if _, err := os.Stat(file); err != nil {
		return 0 // deleted or never written: nothing to format
	}

	projectDir, ok := cleanAbs(os.Getenv("CLAUDE_PROJECT_DIR"))
	if !ok {
		if projectDir, ok = cleanAbs(in.Cwd); !ok {
			fmt.Fprintln(stderr, "fmthook: no absolute project directory in CLAUDE_PROJECT_DIR or hook input")
			return 1
		}
	}
	// G703: projectDir comes from Claude Code itself and was checked by cleanAbs; this only tests for a folder.
	_, statErr := os.Stat(filepath.Join(projectDir, "web", "node_modules")) //nolint:gosec // see above

	// Bound the whole run so a stuck tool can't hang the editing session.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var problems []string
	for _, c := range fmthook.Commands(file, projectDir, statErr == nil) {
		cmd := exec.CommandContext(ctx, c.Name, c.Args...) //nolint:gosec // G204: programs and args come from fmthook.Commands, not from user input
		cmd.Dir = c.Dir
		var stdout, out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if c.OnlyFile != "" {
			cmd.Stdout = &stdout // the JSON report; text output goes to out via stderr
		}

		err := cmd.Run()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exitErr):
			if p := problem(c, stdout.Bytes(), out.String()); p != "" {
				problems = append(problems, p)
			}
		default:
			fmt.Fprintf(stderr, "fmthook: could not run %s: %v\n", c.Name, err)
			return 1
		}
	}

	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "fmthook: timed out after 90s")
		return 1
	}
	if len(problems) > 0 {
		fmt.Fprintf(stderr, "Formatting/lint issues after editing %s. Fix them at the source:\n\n%s\n",
			file, strings.Join(problems, "\n\n"))
		return 2
	}
	return 0
}

// problem describes a failed command for Claude, or returns "" when a filtered lint run only
// found issues in files other than the edited one.
func problem(c fmthook.Command, report []byte, output string) string {
	header := c.Name + " " + strings.Join(c.Args, " ") + ":\n"
	if c.OnlyFile == "" {
		return header + strings.TrimSpace(output)
	}
	issues, total, err := fmthook.IssuesFor(report, c.OnlyFile)
	switch {
	case err != nil || total == 0:
		// No usable report: golangci-lint itself failed, e.g. the package doesn't type-check.
		return header + strings.TrimSpace(output)
	case len(issues) == 0:
		return "" // issues only in other files of the package; go tool task check reports those
	}
	return header + strings.Join(issues, "\n")
}

// cleanAbs returns dir cleaned, and false unless it is a non-empty absolute path.
func cleanAbs(dir string) (string, bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	return filepath.Clean(dir), true
}
