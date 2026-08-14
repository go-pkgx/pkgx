package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

func TestRunConfigWarning(t *testing.T) {
	old := configError
	defer func() { configError = old }()
	configError = func() error { return errors.New("bad config") }
	// The warning is emitted to stderr; the command still runs to completion.
	if rc := run([]string{"-v"}); rc != 0 {
		t.Errorf("-v with config warning rc=%d", rc)
	}
}

func TestSplitPlus(t *testing.T) {
	cases := []struct {
		argv       []string
		plus, rest []string
	}{
		// no +pkg: the first token is the package to run.
		{[]string{"node@22", "--version"}, nil, []string{"node@22", "--version"}},
		// +pkgs then a command.
		{[]string{"+git", "+gnu.org/bash", "sh", "-c", "x"}, []string{"git", "gnu.org/bash"}, []string{"sh", "-c", "x"}},
		// "--" ends the package list and is dropped.
		{[]string{"+git", "--", "git", "--version"}, []string{"git"}, []string{"git", "--version"}},
		// +pkgs with no command.
		{[]string{"+wget"}, []string{"wget"}, nil},
		// lone "--" then a bare command.
		{[]string{"--", "env"}, nil, []string{"env"}},
	}
	for _, c := range cases {
		plus, rest := splitPlus(c.argv)
		if !eqStrs(plus, c.plus) || !eqStrs(rest, c.rest) {
			t.Errorf("splitPlus(%v) = %v,%v want %v,%v", c.argv, plus, rest, c.plus, c.rest)
		}
	}
}

func TestSpecParsing(t *testing.T) {
	cases := []struct{ in, proj, con string }{
		{"nodejs.org", "nodejs.org", "*"},
		{"node@22", "node", "22"},
		{"gnu.org/wget@^1.21", "gnu.org/wget", "^1.21"},
		{"openssl.org@=3.0.0", "openssl.org", "=3.0.0"},
	}
	for _, c := range cases {
		if p := project(c.in); p != c.proj {
			t.Errorf("project(%q)=%q want %q", c.in, p, c.proj)
		}
		if con := constraint(c.in); con != c.con {
			t.Errorf("constraint(%q)=%q want %q", c.in, con, c.con)
		}
	}
}

func TestRunHelpVersion(t *testing.T) {
	if rc := run([]string{"--help"}); rc != 0 {
		t.Errorf("--help rc=%d", rc)
	}
	if rc := run([]string{"-v"}); rc != 0 {
		t.Errorf("-v rc=%d", rc)
	}
	if rc := run(nil); rc != 2 {
		t.Errorf("no-args rc=%d want 2", rc)
	}
}

func eqStrs(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// TestEnvMode: `pkgx +a +b` with no command prints the environment those
// packages bring — the `eval "$(pkgx …)"` contract bk's build script depends on.
// It must cover the WHOLE closure and BOTH lib and lib64 (a linux/x86-64 bottle
// may install its libraries and .pc files under lib64; upstream pkgx exports
// only lib/pkgconfig, which is why a wget build could not find openssl there).
func TestEnvMode(t *testing.T) {
	dir := t.TempDir()
	mk := func(t *testing.T, parts ...string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(append([]string{dir}, parts...)...), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// tool.org: the classic lib layout; dep.org: the lib64 one.
	mk(t, "tool.org", "v1.0.0", "bin")
	mk(t, "tool.org", "v1.0.0", "lib", "pkgconfig")
	mk(t, "tool.org", "v1.0.0", "include")
	mk(t, "tool.org", "v1.0.0", "share", "aclocal")
	mk(t, "dep.org", "v2.0.0", "lib64", "pkgconfig")

	closure := []bottle.Resolved{
		{Project: "tool.org", Version: bottle.ParseVer("1.0.0")},
		{Project: "dep.org", Version: bottle.ParseVer("2.0.0")},
	}
	var out strings.Builder
	if err := envMode(closure, dir, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`export PATH="` + filepath.Join(dir, "tool.org/v1.0.0/bin") + `${PATH:+:$PATH}"`,
		filepath.Join(dir, "dep.org/v2.0.0/lib64/pkgconfig"), // the lib64 case
		filepath.Join(dir, "tool.org/v1.0.0/lib/pkgconfig"),  // and the lib one
		filepath.Join(dir, "tool.org/v1.0.0/include"),        // CPATH
		filepath.Join(dir, "tool.org/v1.0.0/share/aclocal"),  // ACLOCAL_PATH
		"export LD_LIBRARY_PATH=", "export LIBRARY_PATH=", "export XDG_DATA_DIRS=",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// every line must be eval-able and preserve any pre-existing value
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if !strings.HasPrefix(line, "export ") || !strings.HasSuffix(line, `"`) {
			t.Errorf("not an eval-able assignment: %q", line)
		}
	}
	// a package contributing nothing emits no variable at all
	var empty strings.Builder
	if err := envMode(nil, dir, &empty); err != nil || empty.String() != "" {
		t.Errorf("empty closure → %q, %v", empty.String(), err)
	}
}

// TestAddDir: only existing directories join a path, once, and a file never does.
func TestAddDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "d")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var list []string
	addDir(&list, sub)
	addDir(&list, sub)                          // duplicate
	addDir(&list, file)                         // not a directory
	addDir(&list, filepath.Join(dir, "absent")) // does not exist
	if len(list) != 1 || list[0] != sub {
		t.Fatalf("list = %v, want just %q", list, sub)
	}
}

// TestResolveBinExplicitPath: `pkgx +pkg -- /abs/path` runs that path, which is
// how a tool that is NOT one of the packages (bk in a scratch builder) gets a
// runtime environment from them.
func TestResolveBinExplicitPath(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "tool")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveBin("", prog, nil, nil, dir)
	if err != nil || got != prog {
		t.Fatalf("resolveBin = %q, %v", got, err)
	}
	if _, err := resolveBin("", filepath.Join(dir, "absent"), nil, nil, dir); err == nil {
		t.Fatal("want an error for a path that is not there")
	}
}
