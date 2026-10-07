package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE DEFECT THIS TEST PINS. The first version of catstat declared the JSON
// shape by hand and asked for `dependencies`. The field is `deps`, so it
// found none, and was about to report "0% of projects have dependencies"
// about a catalogue in which half do.
//
// The fixture therefore uses the field names the FACTORY writes, taken from
// a published catalogue, not the ones a reader might expect.
const published = `{"generated":"2026-10-06T15:04:06Z","projects":[
 {"project":"abseil.io","versions":["20260526.0"],"platforms":["linux/aarch64"],"deps":["gnu.org/gcc/libstdcxx"]},
 {"project":"stedolan.github.io/jq","versions":["1.8.2"],"platforms":["linux/aarch64"],"provides":["jq"],"summary":"a JSON processor"},
 {"project":"langchain.com","provides":["jsondiff","langchain"]},
 {"project":"example.com","platforms":["linux/aarch64"]}
]}`

func TestItCountsTheFieldTheFactoryWrites(t *testing.T) {
	out := run(t, published)
	for _, want := range []string{
		"projects      4",
		// One of four declares deps. Read through the wrong field name
		// this line says 0, and says it with the same confidence.
		"dependencies  1 (25.0%)",
		"summaries     1 (25.0%)",
		"provides      2 (50.0%)",
		// A project named by the pantry with nothing published is NOT
		// bottled — that distinction is the whole point of `no bottle
		// here`. The fourth entry carries a PLATFORM and no version, so
		// counting platforms instead of versions gives 3 here; the
		// factory happens never to emit that pair today, which is exactly
		// why the fixture has to, or the definition is pinned by nothing.
		"with a bottle 2 (50.0%)",
		"commands      3",
		"namespaces    1",
		"platforms     linux/aarch64",
		"generated     2026-10-06T15:04:06Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
}

// AN EMPTY CATALOGUE IS A BROKEN FILE, not a census of nothing. Reported as
// 0 projects it would print a column of 0.0%% — a division by zero dressed
// up as a finding — and a reader would take it for a pantry that has lost
// its contents rather than a file that failed to arrive.
func TestAnEmptyCatalogueIsRefused(t *testing.T) {
	err := report(&strings.Builder{}, write(t, `{"generated":"2026-10-06T15:04:06Z","projects":[]}`))
	if err == nil {
		t.Fatal("an empty catalogue was censused")
	}
	if !strings.Contains(err.Error(), "no projects") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

// IT GOES THROUGH bottle's PARSER, so a catalogue bottle's own refusals are
// this tool's refusals too: nonsense is reported, not counted as zero.
func TestNonsenseIsReportedNotCounted(t *testing.T) {
	if err := report(&strings.Builder{}, write(t, "not json at all")); err == nil {
		t.Fatal("garbage was censused without complaint")
	}
	if err := report(&strings.Builder{}, filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a missing file was censused without complaint")
	}
}

func run(t *testing.T, body string) string {
	t.Helper()
	var b strings.Builder
	if err := report(&b, write(t, body)); err != nil {
		t.Fatalf("report: %v", err)
	}
	return b.String()
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
