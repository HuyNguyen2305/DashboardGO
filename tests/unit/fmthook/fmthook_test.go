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
				"golangci-lint run --output.json.path=stdout --output.text.path=stderr --show-stats=false ./internal/service/ @.",
			},
		},
		{
			name: "go file in the project root",
			file: in("main.go"),
			want: []string{"gofmt -w <project>/main.go @.", "golangci-lint run --output.json.path=stdout --output.text.path=stderr --show-stats=false ./ @."},
		},
		{
			name: "relative path is resolved against the project",
			file: "cmd/api/main.go",
			want: []string{"gofmt -w <project>/cmd/api/main.go @.", "golangci-lint run --output.json.path=stdout --output.text.path=stderr --show-stats=false ./cmd/api/ @."},
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
	assert.Equal(t, "./internal/dto/", cmds[1].Args[len(cmds[1].Args)-1])
	assert.Equal(t, "internal/dto/error.go", cmds[1].OnlyFile)
}

func TestLintIsFilteredToTheEditedFile(t *testing.T) {
	project := t.TempDir()
	cmds := fmthook.Commands(filepath.Join(project, "internal", "service", "dashboard.go"), project, false)
	require.Len(t, cmds, 2)
	assert.Equal(t, "internal/service/dashboard.go", cmds[1].OnlyFile)
	assert.NotContains(t, cmds[1].Args, "--fix", "lint must not rewrite other files of the package")
	assert.Empty(t, cmds[0].OnlyFile, "gofmt output is never filtered")
}

func TestIssuesFor(t *testing.T) {
	report := `{"Issues":[
		{"FromLinter":"errcheck","Text":"Error return value is not checked","Pos":{"Filename":"internal/service/dashboard.go","Line":12,"Column":3}},
		{"FromLinter":"gosec","Text":"G104","Pos":{"Filename":"internal\\service\\other.go","Line":4,"Column":1}},
		{"FromLinter":"misspell","Text":"misspelled","Pos":{"Filename":"internal\\service\\dashboard.go","Line":30,"Column":9}}
	],"Report":{}}`
	cases := []struct {
		name, report, file string
		want               []string
		wantTotal          int
		wantErr            bool
	}{
		{"keeps the edited file, in either slash style", report, "internal/service/dashboard.go", []string{
			"internal/service/dashboard.go:12:3: Error return value is not checked (errcheck)",
			"internal/service/dashboard.go:30:9: misspelled (misspell)",
		}, 3, false},
		{"issues only in other files", report, "internal/service/stats.go", nil, 3, false},
		{"clean report", `{"Issues":[],"Report":{}}`, "a.go", nil, 0, false},
		{"empty output means golangci-lint failed", "", "a.go", nil, 0, true},
		{"text instead of JSON", "level=error msg=typechecking error", "a.go", nil, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, err := fmthook.IssuesFor([]byte(tc.report), tc.file)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantTotal, total)
		})
	}
}
