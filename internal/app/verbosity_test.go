package app

import (
	"slices"
	"testing"
)

// TestStripVerbosity covers the flags upstream pkgx accepts and this fork did
// not, and — more importantly — the ones it must NOT swallow.
//
// The breakage this fixes: `pkgx --quiet deno^2.1 …` is the shebang of
// upstream's own `pkgm`, and with this binary as the `pkgx` on PATH it came
// back as `no project declares the command "--quiet"`. The flag was being
// looked up as a COMMAND NAME, so the failure pointed at the registry instead
// of at the argument.
//
// Reference: pkgxdev/pkgx `crates/cli/src/args.rs`, read 2026-10-31 — the long
// forms `--quiet`/`--silent` plus single-dash clusters of `q h s j v ! Q C`.
func TestStripVerbosity(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "the shebang that broke",
			in:   []string{"--quiet", "deno^2.1", "run", "x.ts"},
			want: []string{"deno^2.1", "run", "x.ts"},
		},
		{"--silent", []string{"--silent", "+go.dev"}, []string{"+go.dev"}},
		{"-q", []string{"-q", "gnu.org/bash"}, []string{"gnu.org/bash"}},
		{"-s", []string{"-s", "gnu.org/bash"}, []string{"gnu.org/bash"}},
		// Upstream reads a second q as "upgrade quiet to silent"; here both are
		// ineffective, so the only thing that matters is that the cluster is
		// consumed and not treated as a package.
		{"-qq is a cluster", []string{"-qq", "gnu.org/bash"}, []string{"gnu.org/bash"}},
		{"-qs mixed cluster", []string{"-qs", "gnu.org/bash"}, []string{"gnu.org/bash"}},
		{"several in a row", []string{"--quiet", "-s", "--silent", "x"}, []string{"x"}},

		// ⛔ THE FLAGS IT MUST LEAVE ALONE. Eating one of these would be the
		// silent ineffectiveness that splitPlatform's comment rejects, and for
		// -h/-v/-Q it would break three documented behaviours of this fork.
		{"-h is not verbosity", []string{"-h"}, []string{"-h"}},
		{"-v is not verbosity", []string{"-v"}, []string{"-v"}},
		{"-Q is not verbosity", []string{"-Q", "ls"}, []string{"-Q", "ls"}},
		{"--help is not verbosity", []string{"--help"}, []string{"--help"}},
		{"-j is upstream's and not ours", []string{"-j", "+a"}, []string{"-j", "+a"}},
		{"-C is upstream's and not ours", []string{"-C", "/tmp"}, []string{"-C", "/tmp"}},
		{"a cluster with one foreign letter is untouched", []string{"-qj"}, []string{"-qj"}},
		{"--json is a format, not verbosity", []string{"--json", "+a"}, []string{"--json", "+a"}},
		{"--lock is ours", []string{"--lock", "f.hcl"}, []string{"--lock", "f.hcl"}},

		// A package is never a flag, and a bare "-" is not a cluster.
		{"a package survives", []string{"gnu.org/bash"}, []string{"gnu.org/bash"}},
		{"a +spec survives", []string{"+gnu.org/make"}, []string{"+gnu.org/make"}},
		{"a bare dash is left", []string{"-"}, []string{"-"}},
		{"empty stays empty", []string{}, []string{}},

		// ⛔ Only leading, like every other splitter here: a flag after the
		// package belongs to the PACKAGE's own command line.
		{
			name: "it does not reach past the package",
			in:   []string{"gnu.org/bash", "--quiet"},
			want: []string{"gnu.org/bash", "--quiet"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := stripVerbosity(slices.Clone(c.in))
			if !slices.Equal(got, c.want) {
				t.Errorf("stripVerbosity(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestUsageDocumentsThatQuietDoesNothing: accepting a flag that changes no
// output is defensible only while it is SAID. splitPlatform's comment in the
// same file argues that a silently ineffective flag is worse than an absent
// one, so the exception has to stay visible in the help text or it becomes the
// inconsistency that comment warns about.
func TestUsageDocumentsThatQuietDoesNothing(t *testing.T) {
	for _, want := range []string{"--quiet", "--silent", "change NOTHING here"} {
		if !contains(usage, want) {
			t.Errorf("the usage text does not mention %q, so an ineffective flag is undocumented", want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
