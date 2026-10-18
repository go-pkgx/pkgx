// catbench measures what one press of <TAB> costs.
//
// The claim being tested is "completion does no network". A claim like that
// is cheap to assert by reading the code and worthless that way: the only
// witness that survives a refactor is a CLOCK with the network made
// expensive.
//
// So PKGX_DIST points at 10.255.255.1 — a non-routable address, where a
// connect() does not fail, it HANGS until the kernel gives up. A run that
// touches the registry is seconds long and cannot be confused with one that
// does not. Pointing it at a closed local port would have been the wrong
// control: connection-refused comes back in microseconds and would have
// looked exactly like not connecting at all.
//
// Two arms, both measured here rather than one measured and one remembered:
//
//	before  the binary at origin/main, as a user has it today
//	after   this working tree
//
// Each arm gets the arrangement it actually ships with. `before` has no
// catalogue on disk because the concept did not exist; it fetched on every
// press.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const blackhole = "oci://10.255.255.1:5000/pkgx"

type arm struct {
	name string
	bin  string
	env  []string
}

func main() {
	if len(os.Args) != 4 && len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: catbench <worktree> <catalog.json> <runs> [baseline-ref]")
		os.Exit(2)
	}
	tree, catalog := os.Args[1], os.Args[2]
	runs := 0
	fmt.Sscan(os.Args[3], &runs)
	// The baseline is a NAMED ref, not "whatever main is today".
	//
	// The first numbers this tool produced were published against
	// origin/main, and main then moved: running the same command a day
	// later compares two versions that both have the change, reports a
	// handsome 5 ms for the baseline, and quietly means something else.
	// A reader reproducing a published figure must be able to name the
	// commit it came from.
	baseline := "origin/main"
	if len(os.Args) == 5 {
		baseline = os.Args[4]
	}

	tmp, err := os.MkdirTemp("", "catbench")
	must(err)
	defer os.RemoveAll(tmp)

	after := filepath.Join(tmp, "pkgx-after")
	must(build(tree, "", after))
	before := filepath.Join(tmp, "pkgx-before")
	must(build(tree, baseline, before))
	// The staging binary. See buildPkgs: `catalog` and `catalog reindex`
	// are pkgs's verbs now, and asking pkgx for them would ask it to RUN a
	// package called catalog.
	stager := filepath.Join(tmp, "pkgs")
	must(buildPkgs(tree, stager))

	// The store the "after" arm reads: a real 1907-project catalogue at the
	// path `pkgs catalog update` writes.
	store := filepath.Join(tmp, "store")
	must(os.MkdirAll(store, 0o755))
	dst := catalogPath(stager, store)
	must(os.MkdirAll(filepath.Dir(dst), 0o755))
	b, err := os.ReadFile(catalog)
	must(err)
	must(os.WriteFile(dst, b, 0o644))

	// A SECOND store with the catalogue and nothing else.
	//
	// ⛔ THE BASELINE HAS TO ANSWER THE SAME QUESTION. The arms below used
	// to compare a `before` with an EMPTY store — which completes nothing —
	// against an `after` with a catalogue. The tool printed `(nothing)` in
	// its answer column for that arm and the comparison still read as a
	// regression, because 18 ms of answering nothing beats 25 ms of
	// answering. Those arms were right for the change that INTRODUCED the
	// catalogue and wrong for every change after it.
	//
	// So: json holds the catalogue only, and indexed holds the catalogue
	// plus the completion name index. Both complete `gnu.o`; the difference
	// between them is the one thing under test.
	jsonOnly := filepath.Join(tmp, "store-json")
	must(os.MkdirAll(filepath.Dir(catalogPath(stager, jsonOnly)), 0o755))
	must(os.WriteFile(catalogPath(stager, jsonOnly), b, 0o644))

	// The index, built the way a user's machine builds it — by asking the
	// binary under test, not by writing the format here. A benchmark that
	// produced the file itself would be a second implementation of it, and
	// the first thing to drift.
	must(reindex(stager, store))

	empty := filepath.Join(tmp, "empty")
	must(os.MkdirAll(empty, 0o755))

	arms := []arm{
		// THE BASELINE THAT ANSWERS: the shipped binary with a catalogue on
		// disk, which is what a user who has run `catalog update` has. Every
		// keystroke decodes the whole JSON here.
		{"before, catalogue (" + baseline + ")", before, []string{"PKGX_DIR=" + jsonOnly, "PKGX_DIST=" + blackhole}},
		// The target: catalogue plus name index.
		{"after, catalogue + index", after, []string{"PKGX_DIR=" + store, "PKGX_DIST=" + blackhole}},
		// ⛔ THE FALLBACK, MEASURED. The new binary against a store that has
		// the catalogue and NO index — an image that shipped one file, or a
		// store half-refreshed. It must answer the same thing, and it must
		// not be slower than the baseline: the index may only add a faster
		// path, never a cost on the path without it.
		{"after, index removed (falls back)", after, []string{"PKGX_DIR=" + jsonOnly, "PKGX_DIST=" + blackhole}},
		// The two arms that answer NOTHING, kept because they are the
		// original question this tool was built for — "completion does no
		// network" — and because the blackhole is what makes a fetch visible
		// as time. Their answer column reads `(nothing)`, which is how a
		// reader knows not to compare them with the three above.
		{"no catalogue, blackholed", before, []string{"PKGX_DIR=" + empty, "PKGX_DIST=" + blackhole}},
		{"no catalogue, real registry", before, []string{"PKGX_DIR=" + empty}},
	}

	report(os.Stdout, arms, runs)
}

// report is the whole measurement, separated from main so a test can drive
// it with arms it controls. A measuring tool that is never itself measured
// is how a plausible wrong number gets published.
func report(w io.Writer, arms []arm, runs int) {
	fmt.Fprintf(w, "%d runs of: PKGX_GET_COMPLETIONS=0 pkgx gnu.o   (PKGX_DIST=%s)\n\n", runs, blackhole)
	fmt.Fprintf(w, "%-22s %10s %10s %10s   %s\n", "arm", "min", "median", "max", "answer")
	for _, a := range arms {
		var ds []time.Duration
		out := ""
		for i := 0; i < runs; i++ {
			d, o := once(a)
			ds = append(ds, d)
			out = o
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		fmt.Fprintf(w, "%-22s %10s %10s %10s   %s\n",
			a.name, ms(ds[0]), ms(ds[len(ds)/2]), ms(ds[len(ds)-1]), describe(out))
	}
}

// once runs a single completion and times the whole process, because a
// whole process is what a <TAB> costs. Timing the function inside would
// measure something nobody experiences.
func once(a arm) (time.Duration, string) {
	cmd := exec.Command(a.bin, "gnu.o")
	cmd.Env = append(append(os.Environ(), a.env...), "PKGX_GET_COMPLETIONS=0", "PKGX_CATALOG=")
	t := time.Now()
	out, _ := cmd.Output()
	return time.Since(t), string(out)
}

func describe(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return "(nothing)"
	}
	lines := strings.Split(out, "\n")
	first := strings.SplitN(lines[0], "\t", 2)[0]
	return fmt.Sprintf("%d candidate(s), first %q", len(lines), first)
}

// buildPkgs builds the `pkgs` binary from the working tree.
//
// ⛔ THE STAGING AND THE MEASUREMENT ARE NOW TWO BINARIES. What is measured
// is still `pkgx gnu.o` — the completion on the hot path. But the staging
// (`catalog` to learn the path, `catalog reindex` to build the index) moved
// to `pkgs` when the verbs did, and a staging step run against pkgx would
// ask it to RUN a package called catalog: the index would never be written,
// the "after" arm would fall back to decoding the JSON, and the two arms
// would agree. That reads as "the change does nothing" rather than as "the
// setup did not happen", which is the one failure this file's comments
// already warn about twice.
//
// Only the working tree's pkgs is built. The baseline arm is an OLD pkgx,
// from before the split, which has no pkgs beside it — and the staging is
// not what is being compared anyway.
func buildPkgs(tree, out string) error {
	c := exec.Command("go", "build", "-o", out, "./cmd/pkgs")
	c.Dir = tree
	c.Stderr = os.Stderr
	return c.Run()
}

func build(tree, rev, out string) error {
	if rev == "" {
		c := exec.Command("go", "build", "-o", out, ".")
		c.Dir = tree
		c.Stderr = os.Stderr
		return c.Run()
	}
	// A worktree, so the control is built from the committed revision and
	// not from whatever is in the editor.
	wt, err := os.MkdirTemp("", "catbench-wt")
	if err != nil {
		return err
	}
	for _, c := range [][]string{
		{"git", "worktree", "add", "--detach", wt, rev},
	} {
		x := exec.Command(c[0], c[1:]...)
		x.Dir = tree
		x.Stderr = os.Stderr
		if err := x.Run(); err != nil {
			return err
		}
	}
	defer func() {
		x := exec.Command("git", "worktree", "remove", "--force", wt)
		x.Dir = tree
		_ = x.Run()
	}()
	c := exec.Command("go", "build", "-o", out, ".")
	c.Dir = wt
	c.Stderr = os.Stderr
	return c.Run()
}

// catalogPath asks the BINARY where its catalogue goes, rather than
// restating the (os, arch) slug table here. A table copied into a measuring
// tool is a dependency nobody updates; the first line of `pkgx catalog` is
// the path, derived by the same code the thing under test uses.
func catalogPath(bin, store string) string {
	cmd := exec.Command(bin, "catalog")
	cmd.Env = append(os.Environ(), "PKGX_DIR="+store, "PKGX_CATALOG=")
	out, err := cmd.Output()
	must(err)
	p := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	if !filepath.IsAbs(p) {
		must(fmt.Errorf("`pkgx catalog` did not name a path: %q", out))
	}
	return p
}

// reindex builds the completion name index for a staged catalogue, by asking
// the binary under test to do it.
//
// Its output is kept and printed on failure rather than discarded: an index
// that was not written makes the "after" arm fall back to decoding the JSON,
// which is the arm it is being compared against — the two would agree, and
// the measurement would look like "the change does nothing" instead of like
// "the setup did not happen".
func reindex(bin, store string) error {
	cmd := exec.Command(bin, "catalog", "reindex")
	cmd.Env = append(os.Environ(), "PKGX_DIR="+store, "PKGX_CATALOG=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("catalog reindex: %w: %s", err, out)
	}
	return nil
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "catbench:", err)
		os.Exit(1)
	}
}
