// Package fmthook decides which formatters and linters to run after Claude edits a file.
package fmthook

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Command is one program to run from Dir. When OnlyFile is set the command is golangci-lint
// writing JSON to stdout, and only issues in that file (project-relative, forward slashes) count;
// see IssuesFor.
type Command struct {
	Name     string
	Args     []string
	Dir      string
	OnlyFile string
}

// generatedDirs hold generated Go code: gofmt still runs there, but lint is skipped.
var generatedDirs = []string{"internal/repository/db/", "tests/mocks/"}

// webExts are the frontend files formatted with ESLint and Prettier.
var webExts = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".json": true, ".css": true}

// Commands returns what to run for an edited file. Files outside projectDir get nothing.
// Frontend files get ESLint and Prettier only when hasNodeModules reports web/node_modules exists.
func Commands(filePath, projectDir string, hasNodeModules bool) []Command {
	rel, ok := relativeTo(filePath, projectDir)
	if !ok {
		return nil
	}
	abs := filepath.Join(projectDir, filepath.FromSlash(rel))

	switch ext := strings.ToLower(path.Ext(rel)); {
	case ext == ".go":
		cmds := []Command{{Name: "gofmt", Args: []string{"-w", abs}, Dir: projectDir}}
		for _, dir := range generatedDirs {
			if strings.HasPrefix(rel, dir) {
				return cmds
			}
		}
		pkg := "./" + path.Dir(rel) + "/"
		if path.Dir(rel) == "." {
			pkg = "./"
		}
		// The whole package is linted so it type-checks, but without --fix (which could rewrite
		// files nobody edited) and with issues filtered to the edited file.
		return append(cmds, Command{
			Name:     "golangci-lint",
			Args:     []string{"run", "--output.json.path=stdout", "--output.text.path=stderr", "--show-stats=false", pkg},
			Dir:      projectDir,
			OnlyFile: rel,
		})

	case strings.HasPrefix(rel, "web/") && webExts[ext] && !strings.HasPrefix(rel, "web/node_modules/"):
		if !hasNodeModules {
			return nil
		}
		webDir := filepath.Join(projectDir, "web")
		return []Command{
			{Name: "npx", Args: []string{"eslint", "--fix", abs}, Dir: webDir},
			{Name: "npx", Args: []string{"prettier", "--write", abs}, Dir: webDir},
		}
	}
	return nil
}

// IssuesFor reads golangci-lint's JSON report and returns the issues in file (project-relative,
// forward slashes) as "file:line:col: text (linter)", plus how many issues the report holds in all.
// An error means the report is unreadable, i.e. golangci-lint itself failed (for example a
// package that doesn't type-check); callers should then show its raw output.
func IssuesFor(report []byte, file string) (forFile []string, total int, err error) {
	var r struct {
		Issues []struct {
			FromLinter string
			Text       string
			Pos        struct {
				Filename     string
				Line, Column int
			}
		}
	}
	if err := json.Unmarshal(report, &r); err != nil {
		return nil, 0, fmt.Errorf("read golangci-lint report: %w", err)
	}
	for _, is := range r.Issues {
		// Windows runs report backslashes; normalize on every OS so reports compare the same.
		name := strings.ReplaceAll(is.Pos.Filename, `\`, "/")
		if strings.EqualFold(name, file) {
			forFile = append(forFile, fmt.Sprintf("%s:%d:%d: %s (%s)", name, is.Pos.Line, is.Pos.Column, is.Text, is.FromLinter))
		}
	}
	return forFile, len(r.Issues), nil
}

// relativeTo returns filePath relative to projectDir with forward slashes,
// or false when the file lies outside the project.
func relativeTo(filePath, projectDir string) (string, bool) {
	if filePath == "" || projectDir == "" {
		return "", false
	}
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(projectDir, filePath)
	}
	rel, err := filepath.Rel(projectDir, filePath)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}
