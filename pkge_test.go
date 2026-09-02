package main

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv drives modeShell without touching the process environment.
func fakeEnv(m map[string]string) func(string) (string, bool) {
	return func(n string) (string, bool) { v, ok := m[n]; return v, ok }
}

// twoPkgs is a composition stub: one prepended path variable and one set one.
func twoPkgs(specs []string) (composed, error) {
	var c composed
	for _, s := range specs {
		c.Env = append(c.Env, envVar{Name: "PATH", Prepend: []string{"/pkgx/" + s + "/bin"}})
	}
	if len(specs) > 0 {
		c.Env = append(c.Env, envVar{Name: "TOOLVAR", Value: "set-by-" + specs[len(specs)-1]})
	}
	return c, nil
}

// A load captures the PRISTINE value of every variable it touches, and says so
// in PKGE_BASE — that snapshot is the whole reason unload can be exact.
func TestLoadCapturesTheBaseOnce(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin"}
	var out strings.Builder
	if err := modeShell("load", []string{"a"}, twoPkgs, fakeEnv(env), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `export PATH='/usr/bin'`) {
		t.Errorf("the base must be restored before the new value:\n%s", got)
	}
	if !strings.Contains(got, "export PKGE_SPECS='a'") {
		t.Errorf("no spec set:\n%s", got)
	}
	// TOOLVAR did not exist: restoring it later must UNSET it, not set it empty.
	if !strings.Contains(got, "unset TOOLVAR") {
		t.Errorf("a variable that did not exist must be restored by unsetting:\n%s", got)
	}
	// A second load must NOT record the first load's output as the base.
	base := betweenQuotes(t, got, "export PKGE_BASE=")
	env2 := map[string]string{"PATH": "/pkgx/a/bin:/usr/bin", "TOOLVAR": "set-by-a", "PKGE_SPECS": "a", "PKGE_BASE": base}
	var out2 strings.Builder
	if err := modeShell("load", []string{"b"}, twoPkgs, fakeEnv(env2), &out2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), `export PATH='/usr/bin'`) {
		t.Errorf("the second load recaptured a polluted base:\n%s", out2.String())
	}
	if !strings.Contains(out2.String(), "export PKGE_SPECS='a b'") {
		t.Errorf("the second load lost the first spec:\n%s", out2.String())
	}
}

// Unloading one of three is EXACT and order-independent: what remains is what
// loading the other two would have produced, not the first load's leftovers.
func TestUnloadIsExact(t *testing.T) {
	var loaded strings.Builder
	if err := modeShell("load", []string{"a", "b", "c"}, twoPkgs, fakeEnv(map[string]string{"PATH": "/usr/bin"}), &loaded); err != nil {
		t.Fatal(err)
	}
	base := betweenQuotes(t, loaded.String(), "export PKGE_BASE=")
	env := map[string]string{
		"PATH": "/pkgx/a/bin:/pkgx/b/bin:/pkgx/c/bin:/usr/bin", "TOOLVAR": "set-by-c",
		"PKGE_SPECS": "a b c", "PKGE_BASE": base,
	}
	var afterUnload strings.Builder
	if err := modeShell("unload", []string{"b"}, twoPkgs, fakeEnv(env), &afterUnload); err != nil {
		t.Fatal(err)
	}
	var direct strings.Builder
	if err := modeShell("load", []string{"a", "c"}, twoPkgs, fakeEnv(map[string]string{"PATH": "/usr/bin"}), &direct); err != nil {
		t.Fatal(err)
	}
	if afterUnload.String() != direct.String() {
		t.Errorf("unload b from {a b c} differs from load {a c}:\n--- unload\n%s\n--- direct\n%s", afterUnload.String(), direct.String())
	}
}

// purge restores everything and forgets the state, so the next load captures a
// fresh base rather than one left over from a previous life of the shell.
func TestPurgeRestoresAndForgets(t *testing.T) {
	var loaded strings.Builder
	if err := modeShell("load", []string{"a"}, twoPkgs, fakeEnv(map[string]string{"PATH": "/usr/bin"}), &loaded); err != nil {
		t.Fatal(err)
	}
	base := betweenQuotes(t, loaded.String(), "export PKGE_BASE=")
	var out strings.Builder
	if err := modeShell("purge", nil, twoPkgs, fakeEnv(map[string]string{"PKGE_SPECS": "a", "PKGE_BASE": base}), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`export PATH='/usr/bin'`, "unset TOOLVAR", "unset PKGE_SPECS", "unset PKGE_BASE"} {
		if !strings.Contains(got, want) {
			t.Errorf("purge is missing %q:\n%s", want, got)
		}
	}
}

// Loading a project already loaded SWAPS it: `load node@22` after `load node@20`
// is what a user means by it, and leaving both would put two nodes on PATH.
func TestLoadSwapsTheSameProject(t *testing.T) {
	got := dedupeSpecs([]string{"nodejs.org@20", "gnu.org/bash", "nodejs.org@22"})
	want := []string{"nodejs.org@22", "gnu.org/bash"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("dedupeSpecs = %v, want %v", got, want)
	}
}

// `unload x` must work whether x was loaded bare or with a constraint —
// otherwise nobody can unload what they loaded.
func TestUnloadByProjectName(t *testing.T) {
	env := map[string]string{"PKGE_SPECS": "nodejs.org@22 gnu.org/bash"}
	var out strings.Builder
	if err := modeShell("unload", []string{"nodejs.org"}, twoPkgs, fakeEnv(env), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "export PKGE_SPECS='gnu.org/bash'") {
		t.Errorf("unload by project name did not drop the constrained spec:\n%s", out.String())
	}
}

// A value containing a quote, a space or a newline must survive the round trip:
// PKGE_BASE is base64 for exactly this reason, and every emission is quoted.
func TestAwkwardValuesSurvive(t *testing.T) {
	nasty := "a b'c\nd$e"
	var out strings.Builder
	if err := modeShell("load", []string{"a"}, twoPkgs, fakeEnv(map[string]string{"PATH": nasty}), &out); err != nil {
		t.Fatal(err)
	}
	base := betweenQuotes(t, out.String(), "export PKGE_BASE=")
	st := loadModState(func(n string) string {
		if n == "PKGE_BASE" {
			return base
		}
		return ""
	})
	if st.base["PATH"] != nasty {
		t.Errorf("PATH round-tripped as %q, want %q", st.base["PATH"], nasty)
	}
	// And the restore line must be a shell word that reproduces it.
	var restore strings.Builder
	st.restoreScript(&restore)
	if !strings.Contains(restore.String(), `export PATH='a b'\''c`) {
		t.Errorf("the quote was not escaped:\n%s", restore.String())
	}
}

// Unreadable state is treated as no state: a shell with a corrupted
// PKGE_BASE must still be usable, not permanently broken.
func TestCorruptBaseIsIgnored(t *testing.T) {
	st := loadModState(func(n string) string {
		if n == "PKGE_BASE" {
			return "not base64 !!"
		}
		return ""
	})
	if len(st.base) != 0 {
		t.Errorf("a corrupt base must decode to nothing, got %v", st.base)
	}
}

// The shell function is evaluated by a real shell, not just printed: a syntax
// error in it would break every login that sources it.
func TestShellInitIsValidShell(t *testing.T) {
	sh, err := osexec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	var out strings.Builder
	if err := modeShellInit(&out); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "init.sh")
	if err := os.WriteFile(f, []byte(out.String()+"\npkge help 2>/dev/null\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := osexec.Command(sh, "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("the shell function does not parse: %v\n%s", err, b)
	}
}

// betweenQuotes pulls the single-quoted value that follows prefix.
func betweenQuotes(t *testing.T, s, prefix string) string {
	t.Helper()
	i := strings.Index(s, prefix)
	if i < 0 {
		t.Fatalf("no %q in:\n%s", prefix, s)
	}
	rest := s[i+len(prefix)+1:]
	j := strings.Index(rest, "'")
	if j < 0 {
		t.Fatalf("unterminated quote after %q", prefix)
	}
	return rest[:j]
}
