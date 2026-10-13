package fmthook_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dashboardgo/internal/devtools/fmthook"
)

// summary renders commands as "name args… @dir-relative-to-project" for compact assertions.
func summary(t *testing.T, project string, cmds []fmthook.Command) []string {
	t.Helper()
	out := make([]string, len(cmds))
	for i, c := range cmds {
		dir, err := filepath.Rel(project, c.Dir)
		require.NoError(t, err)
		args := make([]string, len(c.Args))
		for j, a := range c.Args {
			if rel, err := filepath.Rel(project, a); err == nil && filepath.IsAbs(a) {
				a = "<project>/" + filepath.ToSlash(rel)
			}
			args[j] = a
		}
		out[i] = c.Name + " " + strings.Join(args, " ") + " @" + filepath.ToSlash(dir)
	}
	return out
}

func TestCommands(t *testing.T) {
	project := t.TempDir()
	in := func(rel string) string { return filepath.Join(project, filepath.FromSlash(rel)) }

	cases := []struct {
		name           string
		file           string
		hasNodeModules bool
		want           []string
	}{
		{
			name: "go file gets gofmt and lint on its package",
			file: in("internal/service/dashboard.go"),
			want: []string{
				"gofmt -w <project>/internal/service/dashboard.go @.",
				"golangci-lint run --fix ./internal/service/ @.",
			},
		},
		{
			name: "go file in the project root",
			file: in("main.go"),
			want: []string{"gofmt -w <project>/main.go @.", "golangci-lint run --fix ./ @."},
		},
		{
			name: "relative path is resolved against the project",
			file: "cmd/api/main.go",
			want: []string{"gofmt -w <project>/cmd/api/main.go @.", "golangci-lint run --fix ./cmd/api/ @."},
		},
		{
			name: "sqlc output gets gofmt only",
			file: in("internal/repository/db/models.go"),
			want: []string{"gofmt -w <project>/internal/repository/db/models.go @."},
		},
		{
			name: "mocks get gofmt only",
			file: in("tests/mocks/dashboard_service.go"),
			want: []string{"gofmt -w <project>/tests/mocks/dashboard_service.go @."},
		},
		{
			name:           "frontend file without node_modules is skipped",
			file:           in("web/src/App.tsx"),
			hasNodeModules: false,
			want:           []string{},
		},
		{
			name:           "frontend file with node_modules gets eslint and prettier",
			file:           in("web/src/App.tsx"),
			hasNodeModules: true,
			want: []string{
				"npx eslint --fix <project>/web/src/App.tsx @web",
				"npx prettier --write <project>/web/src/App.tsx @web",
			},
		},
		{
			name:           "files inside node_modules are never touched",
			file:           in("web/node_modules/pkg/index.js"),
			hasNodeModules: true,
			want:           []string{},
		},
		{
			name:           "json outside web is skipped",
			file:           in("tsconfig.json"),
			hasNodeModules: true,
			want:           []string{},
		},
		{"markdown is skipped", in("CLAUDE.md"), false, []string{}},
		{"sql is skipped", in("migrations/000001_init_schema.up.sql"), false, []string{}},
		{"file outside the project is skipped", filepath.Join(filepath.Dir(project), "elsewhere", "x.go"), false, []string{}},
		{"empty path is skipped", "", false, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summary(t, project, fmthook.Commands(tc.file, project, tc.hasNodeModules))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCommandsHandlesWindowsPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("backslash paths and drive letters only exist on Windows")
	}
	// Claude Code may report a lower-case drive letter while the project dir uses upper case.
	cmds := fmthook.Commands(`e:\DashboardGO\internal\dto\error.go`, `E:\DashboardGO`, false)
	require.Len(t, cmds, 2)
	assert.Equal(t, "gofmt", cmds[0].Name)
	assert.Equal(t, []string{"run", "--fix", "./internal/dto/"}, cmds[1].Args)
}
