package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRequestedSessionWorkDir(t *testing.T) {
	target := t.TempDir()
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", target, err)
	}

	linkRoot := t.TempDir()
	link := filepath.Join(linkRoot, "worktree")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(%q, %q): %v", target, link, err)
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("test"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", file, err)
	}

	missing := filepath.Join(t.TempDir(), "missing")

	tests := []struct {
		name        string
		requested   string
		want        string
		wantErrPart string
	}{
		{name: "omitted", requested: " \t\n", want: ""},
		{name: "absolute directory", requested: target, want: canonicalTarget},
		{name: "symlinked directory", requested: "  " + link + "  ", want: canonicalTarget},
		{name: "relative path", requested: "relative/worktree", wantErrPart: "absolute"},
		{name: "missing path", requested: missing, wantErrPart: "resolving requested session work directory"},
		{name: "regular file", requested: file, wantErrPart: "not a directory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRequestedSessionWorkDir(tt.requested)
			if tt.wantErrPart != "" {
				if err == nil {
					t.Fatalf("resolveRequestedSessionWorkDir(%q) error = nil, want containing %q", tt.requested, tt.wantErrPart)
				}
				if !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("resolveRequestedSessionWorkDir(%q) error = %q, want containing %q", tt.requested, err, tt.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRequestedSessionWorkDir(%q): %v", tt.requested, err)
			}
			if got != tt.want {
				t.Fatalf("resolveRequestedSessionWorkDir(%q) = %q, want %q", tt.requested, got, tt.want)
			}
		})
	}
}
