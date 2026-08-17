package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		// A range operator appended straight to the project is pkgx syntax too,
		// and it is what a pantry dependency map renders as. Reading it as part
		// of the project name asks the registry for "…/ncurses^6" and the whole
		// dependency environment dies with "invalid repository".
		{"invisible-island.net/ncurses^6", "invisible-island.net/ncurses", "^6"},
		{"cmake.org~3.30", "cmake.org", "~3.30"},
		{"gnu.org/gmp>=6", "gnu.org/gmp", ">=6"},
		{"llvm.org<19", "llvm.org", "<19"},
		{"zlib.net=1.3.1", "zlib.net", "=1.3.1"},
		// Degenerate forms: nothing before the delimiter is not a constraint,
		// and an empty constraint pins nothing.
		{"@22", "@22", "*"},
		{"node@", "node", ""},
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
	bottle.Warn = nil
	if rc := run([]string{"--help"}); rc != 0 {
		t.Errorf("--help rc=%d", rc)
	}
	// run() installs the diagnostic sink: a soname the resolver cannot provide
	// must reach the user's stderr, not vanish until the program fails to start.
	if bottle.Warn == nil {
		t.Error("run() must install bottle.Warn")
	}
	bottle.Warn("libnope.so.9 is NEEDED but nothing provides it") // must not panic
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
	mk(t, "tool.org", "v1.0.0", "sbin")
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
		// bin AND sbin: a recipe calling ldconfig (glibc ships it in sbin) got a
		// bare exit 127 without it.
		`export PATH="` + filepath.Join(dir, "tool.org/v1.0.0/bin") + ":" + filepath.Join(dir, "tool.org/v1.0.0/sbin") + `${PATH:+:$PATH}"`,
		filepath.Join(dir, "dep.org/v2.0.0/lib64/pkgconfig"), // the lib64 case
		filepath.Join(dir, "tool.org/v1.0.0/lib/pkgconfig"),  // and the lib one
		filepath.Join(dir, "tool.org/v1.0.0/include"),        // CPATH
		filepath.Join(dir, "tool.org/v1.0.0/share/aclocal"),  // ACLOCAL_PATH
		"export LD_LIBRARY_PATH=", "export LIBRARY_PATH=", "export XDG_DATA_DIRS=",
		// CMake ignores CPATH/LIBRARY_PATH entirely; find_package walks
		// CMAKE_PREFIX_PATH, so the closure's PREFIXES have to be there or a
		// cmake recipe cannot see a dependency that is plainly installed.
		`export CMAKE_PREFIX_PATH="` + filepath.Join(dir, "tool.org/v1.0.0") + ":" +
			filepath.Join(dir, "dep.org/v2.0.0") + `${CMAKE_PREFIX_PATH:+:$CMAKE_PREFIX_PATH}"`,
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

// TestEnvModeRuntimeEnv: a package's own `runtime: env:` reaches the emitted
// environment. help2man bundles Locale::gettext in its prefix and publishes
// PERL5LIB; without this, help2man dies with "Can't locate Locale/gettext.pm"
// and takes its consumer's build with it.
func TestEnvModeRuntimeEnv(t *testing.T) {
	dir := t.TempDir()
	for _, p := range [][]string{{"gnu.org/help2man", "v1.49.3", "bin"}, {"plain.org", "v1.0.0", "bin"}} {
		if err := os.MkdirAll(filepath.Join(dir, p[0], p[1], p[2]), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakePantry(t, map[string]string{
		"gnu.org/help2man": "runtime:\n  env:\n    PERL5LIB: \"{{prefix}}/lib/perl5:$PERL5LIB\"\nprovides:\n  - bin/help2man\n",
		"plain.org":        "provides:\n  - bin/plain\n",
	})

	closure := []bottle.Resolved{
		{Project: "gnu.org/help2man", Version: bottle.ParseVer("1.49.3")},
		{Project: "plain.org", Version: bottle.ParseVer("1.0.0")},
	}
	var out strings.Builder
	if err := envMode(closure, dir, &out); err != nil {
		t.Fatal(err)
	}
	want := `export PERL5LIB="` + filepath.Join(dir, "gnu.org/help2man/v1.49.3") + `/lib/perl5:$PERL5LIB"`
	if !strings.Contains(out.String(), want) {
		t.Errorf("missing %q in:\n%s", want, out.String())
	}
	// the computed paths still come first, so a package's own declaration can
	// build on them rather than be overwritten by them
	if strings.Index(out.String(), "export PATH=") > strings.Index(out.String(), "export PERL5LIB=") {
		t.Error("runtime env must be emitted after the computed paths")
	}
}

// A recipe the resolver cannot fetch must not sink the environment: the package
// is installed and its paths are already exported.
func TestEnvModeRuntimeEnvUnfetchable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ghost.org", "v9.9.9", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakePantry(t, map[string]string{})
	var said []string
	bottle.Warn = func(msg string) { said = append(said, msg) }
	defer func() { bottle.Warn = nil }()

	var out strings.Builder
	if err := envMode([]bottle.Resolved{{Project: "ghost.org", Version: bottle.ParseVer("9.9.9")}}, dir, &out); err != nil {
		t.Fatalf("an unfetchable recipe must not fail the env: %v", err)
	}
	if !strings.Contains(out.String(), "export PATH=") {
		t.Error("the computed paths must still be emitted")
	}
	if len(said) != 1 || !strings.Contains(said[0], "ghost.org") {
		t.Errorf("the failure must be said, got %v", said)
	}
}

// fakePantry serves package.yml for the given projects and points bottle at it,
// so a test can drive recipe-reading without the network.
func fakePantry(t *testing.T, recipes map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proj := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/package.yml")
		if y, ok := recipes[proj]; ok {
			fmt.Fprint(w, y)
			return
		}
		http.NotFound(w, r)
	}))
	old, oldOverlay := bottle.PantryBase, bottle.PantryOverlay
	bottle.PantryBase, bottle.PantryOverlay = srv.URL, ""
	t.Cleanup(func() { srv.Close(); bottle.PantryBase, bottle.PantryOverlay = old, oldOverlay })
}
