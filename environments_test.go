package main

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnvFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadEnvironments(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, "site.hcl2", `
env "cfd" {
  description = "the solver stack"
  packages    = ["openmpi.org@5", "hdf5.org"]
}
env "tools" {
  packages = ["gnu.org/sed"]
}
`)
	var warns []string
	envs := loadEnvironments([]string{dir}, func(m string) { warns = append(warns, m) })
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	if len(envs) != 2 {
		t.Fatalf("got %d environments, want 2", len(envs))
	}
	cfd := envs["cfd"]
	if cfd.Description != "the solver stack" || strings.Join(cfd.Packages, " ") != "openmpi.org@5 hdf5.org" {
		t.Errorf("cfd = %+v", cfd)
	}
	if cfd.File == "" {
		t.Error("an environment must remember where it was declared: on a shared system that is the first question asked")
	}
}

// A file that does not parse is REPORTED and skipped. One broken site file must
// not make every other environment disappear, and silence would look exactly
// like an environment that was never declared.
func TestBrokenFileIsReportedNotFatal(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, "a-broken.hcl2", `env "x" { packages = [`)
	writeEnvFile(t, dir, "b-good.hcl2", `env "ok" { packages = ["gnu.org/sed"] }`)
	var warns []string
	envs := loadEnvironments([]string{dir}, func(m string) { warns = append(warns, m) })
	if len(warns) == 0 {
		t.Error("a broken file must be reported")
	}
	if _, ok := envs["ok"]; !ok {
		t.Error("a broken file must not hide the healthy ones")
	}
}

// Shapes that are wrong rather than unparseable: an env with no package, and
// packages that are not a list of strings.
func TestRejectedShapes(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, "x.hcl2", `
env "empty"  { packages = [] }
env "wrong"  { packages = "gnu.org/sed" }
env "fine"   { packages = ["gnu.org/sed"] }
`)
	var warns []string
	envs := loadEnvironments([]string{dir}, func(m string) { warns = append(warns, m) })
	if len(envs) != 1 {
		t.Errorf("got %v, want only fine", envs)
	}
	if len(warns) != 2 {
		t.Errorf("both bad shapes must be named, got %v", warns)
	}
}

// A later directory wins, so a user's own file overrides a site's.
func TestNearestDirectoryWins(t *testing.T) {
	site, user := t.TempDir(), t.TempDir()
	writeEnvFile(t, site, "s.hcl2", `env "cfd" { packages = ["site.example"] }`)
	writeEnvFile(t, user, "u.hcl2", `env "cfd" { packages = ["user.example"] }`)
	envs := loadEnvironments([]string{site, user}, func(string) {})
	if got := envs["cfd"].Packages[0]; got != "user.example" {
		t.Errorf("cfd resolves to %q, want the user's", got)
	}
}

// Loading an environment expands it, while the spec list keeps its NAME — so
// `module list` says cfd, and unloading cfd removes exactly what it brought.
func TestExpandSpecs(t *testing.T) {
	envs := map[string]environment{"cfd": {Name: "cfd", Packages: []string{"openmpi.org@5", "hdf5.org"}}}
	got := expandSpecs([]string{"cfd", "gnu.org/sed"}, envs)
	if strings.Join(got, " ") != "openmpi.org@5 hdf5.org gnu.org/sed" {
		t.Errorf("expandSpecs = %v", got)
	}
	// A name that is not an environment passes through untouched.
	if got := expandSpecs([]string{"nodejs.org@22"}, envs); got[0] != "nodejs.org@22" {
		t.Errorf("a package must not be mistaken for an environment: %v", got)
	}
}

// The module/ml front-end must parse as shell, and must REFUSE to install
// itself where a module system already exists — two implementations answering
// one command is how a support ticket becomes unanswerable.
func TestModuleShim(t *testing.T) {
	sh, err := osexec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	var out strings.Builder
	if err := modeShellInit(true, &out); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "init.sh")
	if err := os.WriteFile(f, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := osexec.Command(sh, "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("the module front-end does not parse: %v\n%s", err, b)
	}
	// With an Lmod present it must decline, out loud, and define nothing.
	script := out.String() + "\nif command -v module >/dev/null 2>&1 || type module >/dev/null 2>&1; then echo DEFINED; else echo ABSENT; fi\n"
	if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := osexec.Command(sh, f)
	cmd.Env = append(os.Environ(), "LMOD_CMD=/opt/lmod/libexec/lmod")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if !strings.Contains(string(b), "ABSENT") {
		t.Errorf("module() was defined despite an existing Lmod:\n%s", b)
	}
	if !strings.Contains(string(b), "not defining module") {
		t.Errorf("declining must be said out loud:\n%s", b)
	}
	// Without one, it defines module and ml.
	cmd = osexec.Command(sh, f)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	if b, err = cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if !strings.Contains(string(b), "DEFINED") {
		t.Errorf("module() was not defined on a machine without one:\n%s", b)
	}
}
