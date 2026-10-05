package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A measuring tool that is never itself measured is how a plausible wrong
// number gets published. These are the two places this one could lie.

// describe is what turns "the completion answered" into a line in a table.
// Getting it wrong in the silent direction is the dangerous one: an arm
// that answered nothing, reported as having answered, makes a fast arm look
// like a working one.
func TestDescribeCannotCallSilenceAnAnswer(t *testing.T) {
	cases := map[string]string{
		"":                              "(nothing)",
		"   \n":                         "(nothing)",
		"gnu.org/\t54 under":            `1 candidate(s), first "gnu.org/"`,
		"gnu.org/bash\t5.3\ngnu.org/m4": `2 candidate(s), first "gnu.org/bash"`,
	}
	for in, want := range cases {
		if got := describe(in); got != want {
			t.Errorf("describe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMs(t *testing.T) {
	if got := ms(1234 * time.Microsecond); got != "1.2 ms" {
		t.Errorf("ms = %q", got)
	}
}

// catalogPath asks the binary under test where its catalogue goes rather
// than restating the (os, arch) slug table. This is the test that the
// question is actually being asked and answered — a slug table copied into
// a measuring tool is a dependency nobody updates, and the first symptom
// would be an arm that silently reads no catalogue and looks wonderfully
// fast.
func TestCatalogPathComesFromTheBinary(t *testing.T) {
	// The BSD lanes cross-compile the test binary on the host and copy it
	// into a VM that has no Go toolchain, so this one cannot build its
	// subject there. Named rather than silently guarded: catbench is a
	// developer tool run where a toolchain exists, and the lanes that have
	// one still run every assertion below.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain in this lane")
	}
	bin := filepath.Join(t.TempDir(), "pkgx")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = ".." + string(filepath.Separator) + ".."
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	store := t.TempDir()
	got := catalogPath(bin, store)
	if !strings.HasPrefix(got, store) {
		t.Fatalf("catalogPath = %q, not under the store %q", got, store)
	}
	if filepath.Ext(got) != ".json" || filepath.Base(filepath.Dir(got)) != "catalog" {
		t.Errorf("catalogPath = %q, want <store>/catalog/<slug>.json", got)
	}
	// And the arm really reads it: put a catalogue there and the binary
	// must complete out of it.
	if err := os.MkdirAll(filepath.Dir(got), 0o755); err != nil {
		t.Fatal(err)
	}
	const cat = `{"generated":"2026-10-05T09:00:00Z","projects":[{"project":"gnu.org/bash","versions":["5.3"]}]}`
	if err := os.WriteFile(got, []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	d, out := once(arm{"t", bin, []string{"PKGX_DIR=" + store, "PKGX_DIST=" + blackhole}})
	if !strings.Contains(out, "gnu.org/") {
		t.Errorf("the arm read no catalogue: %q", out)
	}
	// Not a latency assertion — a loaded CI box is no place for one — but
	// the blackhole makes any connection attempt take seconds, so this
	// separates "did not connect" from "connected quickly".
	if d > 2*time.Second {
		t.Errorf("a completion took %v with PKGX_DIST blackholed: it connected", d)
	}
}

func TestReportPrintsOneLinePerArm(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, []arm{{"a", "/nonexistent/x", nil}, {"b", "/nonexistent/y", nil}}, 2)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	// header, blank, column names, then one per arm
	if len(lines) != 5 {
		t.Fatalf("report printed %d lines:\n%s", len(lines), buf.String())
	}
	for _, want := range []string{"a", "b", "(nothing)"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("report does not mention %q", want)
		}
	}
}
