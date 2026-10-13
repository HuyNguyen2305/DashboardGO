// Package fmthook decides which formatters and linters to run after Claude edits a file.
package fmthook

import (
	"path"
	"path/filepath"
	"strings"
)

// Command is one program to run from Dir.
type Command struct {
	Name string
	Args []string
	Dir  string
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
		return append(cmds, Command{Name: "golangci-lint", Args: []string{"run", "--fix", pkg}, Dir: projectDir})

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
