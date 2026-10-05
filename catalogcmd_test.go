package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

// withFetch answers `pkgx catalog update` without a registry, and counts the
// answers. The count is the point of most of this file: the contract being
// tested is that nothing BUT this command ever asks.
func withFetch(t *testing.T, c bottle.Catalog, err error) *int {
	t.Helper()
	n := 0
	prev := fetchCatalogFor
	fetchCatalogFor = func(osn, arch string) (bottle.Catalog, error) {
		n++
		return c, err
	}
	t.Cleanup(func() { fetchCatalogFor = prev })
	return &n
}

func smallCatalog() bottle.Catalog {
	return bottle.Catalog{
		Generated: "2026-10-05T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "gnu.org/bash", Versions: []string{"5.3"}},
			{Project: "zlib.net", Versions: []string{"1.3.2"}, Deps: []string{"gnu.org/bash"}},
		},
	}
}

// TestUpdateIsTheOnlyThingThatFetches is the whole of phase 1 in one test.
//
// Before this, every <TAB> spawned a pkgx that took an OCI token and pulled
// the catalogue bottle — per press, never reused, because each press is a
// fresh process. The fix is not a cache with a TTL; it is a line: ONE
// command fetches, everything else reads a file. `guix pull` and
// `nix-channel --update` draw it in the same place.
//
// So the assertion is a count, and it is zero everywhere but one line.
func TestUpdateIsTheOnlyThingThatFetches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	t.Setenv("PKGX_CATALOG", "")
	fetches := withFetch(t, smallCatalog(), nil)

	// Reading, with no catalogue on disk: must not fetch, must not fail.
	var out, errb bytes.Buffer
	if code := runCatalogCmd(nil, &out, &errb); code != 0 {
		t.Fatalf("status = %d, %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "pkgx catalog update") {
		t.Errorf("a fresh machine is not told how to fix it: %q", out.String())
	}
	out.Reset()
	if code := runLs(nil, &out, &errb); code != 0 && code != 1 {
		t.Fatalf("ls status = %d", code)
	}
	completionsFor("gnu.o")
	if *fetches != 0 {
		t.Fatalf("%d fetch(es) before any update — completion is back on the network", *fetches)
	}

	// The one command that does.
	out.Reset()
	errb.Reset()
	if code := runCatalogCmd([]string{"update"}, &out, &errb); code != 0 {
		t.Fatalf("update = %d, %s", code, errb.String())
	}
	if *fetches != 1 {
		t.Errorf("update fetched %d time(s), want 1", *fetches)
	}
	osn, arch := bottle.HostSlug()
	want := filepath.Join(dir, "catalog", osn+"-"+arch+".json")
	if !strings.Contains(out.String(), want) {
		t.Errorf("update did not say where it wrote: %q", out.String())
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("no catalogue at %s: %v", want, err)
	}

	// And now reading answers from that file, still without fetching.
	out.Reset()
	errb.Reset()
	if code := runLs([]string{"zlib.net"}, &out, &errb); code != 0 {
		t.Fatalf("ls = %d, %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "gnu.org/bash") {
		t.Errorf("the written catalogue did not round-trip its deps: %q", out.String())
	}
	if !strings.Contains(errb.String(), want) {
		t.Errorf("the header does not name the file it read: %q", errb.String())
	}
	if *fetches != 1 {
		t.Errorf("%d fetch(es) in all, want 1", *fetches)
	}
}

// TestUpdateIsAtomic: a reader that caught a half-written catalogue would
// see a truncated JSON document and report the registry as EMPTY — and the
// reader here is tab-completion, which would do it in silence.
//
// The witness is the order of the calls: the bytes land on a path that is
// not the one anybody reads, and only a rename makes them visible.
func TestUpdateIsAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	withFetch(t, smallCatalog(), nil)

	osn, arch := bottle.HostSlug()
	final := filepath.Join(dir, "catalog", osn+"-"+arch+".json")

	var wrote, renamed []string
	prevW, prevR := osWriteFile, osRename
	osWriteFile = func(name string, b []byte, m os.FileMode) error {
		wrote = append(wrote, name)
		return prevW(name, b, m)
	}
	osRename = func(from, to string) error {
		renamed = append(renamed, from+" -> "+to)
		return prevR(from, to)
	}
	t.Cleanup(func() { osWriteFile, osRename = prevW, prevR })

	if code := runCatalogCmd([]string{"update"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("update failed")
	}
	for _, w := range wrote {
		if w == final {
			t.Errorf("wrote straight onto %s — a reader can see it half done", final)
		}
	}
	if len(renamed) != 1 || !strings.HasSuffix(renamed[0], "-> "+final) {
		t.Errorf("renames = %v, want one onto %s", renamed, final)
	}
}

// TestAFailedUpdateKeepsTheOldCatalogue. An update that cannot reach the
// registry is the normal state of a laptop on a train. Losing yesterday's
// answers to it would make `pkgx catalog update` a command you think twice
// about running, which is the opposite of the point.
func TestAFailedUpdateKeepsTheOldCatalogue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	t.Setenv("PKGX_CATALOG", "")

	withFetch(t, smallCatalog(), nil)
	if code := runCatalogCmd([]string{"update"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("first update failed")
	}
	osn, arch := bottle.HostSlug()
	final := filepath.Join(dir, "catalog", osn+"-"+arch+".json")
	before, err := os.ReadFile(final)
	if err != nil {
		t.Fatal(err)
	}

	withFetch(t, bottle.Catalog{}, errors.New("dial tcp: no route to host"))
	var errb bytes.Buffer
	if code := runCatalogCmd([]string{"update"}, &bytes.Buffer{}, &errb); code != 1 {
		t.Errorf("a failed update exited %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "no route to host") {
		t.Errorf("the reason is not reported: %q", errb.String())
	}
	after, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("the old catalogue is gone: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a failed update changed the catalogue on disk")
	}

	// And it is still readable, so the laptop on the train still completes.
	var out bytes.Buffer
	if code := runLs([]string{"zlib.net"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("ls = %d", code)
	}
}

// TestTheFallbackSentenceMatchesTheState. Two different ways to end up
// showing the local store, and one sentence fits only one of them: telling
// somebody to run `pkgx catalog update` when the file is there and will not
// parse sends them to a command that cannot help.
func TestTheFallbackSentenceMatchesTheState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	t.Setenv("PKGX_CATALOG", "")
	withFetch(t, smallCatalog(), nil)

	var errb bytes.Buffer
	runLs(nil, &bytes.Buffer{}, &errb)
	if !strings.Contains(errb.String(), "no catalogue yet; run: pkgx catalog update") {
		t.Errorf("a fresh machine is not told what to run: %q", errb.String())
	}

	osn, arch := bottle.HostSlug()
	final := filepath.Join(dir, "catalog", osn+"-"+arch+".json")
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("{ truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	runLs(nil, &bytes.Buffer{}, &errb)
	if strings.Contains(errb.String(), "run: pkgx catalog update") {
		t.Errorf("an unparseable catalogue is blamed on never having fetched one: %q", errb.String())
	}
	if !strings.Contains(errb.String(), final) {
		t.Errorf("the unreadable file is not named: %q", errb.String())
	}
}

// TestCatalogStatusReads what update wrote, and says its age — the price of
// an index you refresh by hand is that nobody is told it is stale by magic,
// so every command that reads it says how old it is.
func TestCatalogStatusReads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	withFetch(t, smallCatalog(), nil)
	if code := runCatalogCmd([]string{"update"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("update failed")
	}
	var out bytes.Buffer
	if code := runCatalogCmd(nil, &out, &bytes.Buffer{}); code != 0 {
		t.Fatal("status failed")
	}
	for _, want := range []string{"2 project(s)", "1 with dependencies", "old"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status does not say %q: %q", want, out.String())
		}
	}
}

// TestCatalogIsWiredIntoTheDispatch. A subcommand that works when called
// directly and is unreachable from the command line is not a subcommand. An
// earlier test in this repo tested a function and not the wiring, and the
// wiring was what was broken.
func TestCatalogIsWiredIntoTheDispatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	t.Setenv("PKGX_CATALOG", "")
	withFetch(t, smallCatalog(), nil)

	var code int
	out := captureStdout(t, func() { code = run([]string{"catalog"}) })
	if code != 0 {
		t.Fatalf("pkgx catalog = %d", code)
	}
	if !strings.Contains(out, "pkgx catalog update") {
		t.Errorf("pkgx catalog printed %q", out)
	}

	out = captureStdout(t, func() { code = run([]string{"catalog", "update"}) })
	if code != 0 {
		t.Fatalf("pkgx catalog update = %d", code)
	}
	if !strings.Contains(out, "2 project(s)") {
		t.Errorf("pkgx catalog update printed %q", out)
	}

	// And it is offered at the prompt, or nobody finds it.
	found := false
	for _, c := range completionsFor("cat") {
		if c.value == "catalog" {
			found = true
		}
	}
	if !found {
		t.Error("`pkgx cat<TAB>` does not offer catalog")
	}

	// A third word is a usage error, not a silently ignored argument.
	if code := runCatalogCmd([]string{"update", "extra"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Errorf("pkgx catalog update extra = %d, want 2", code)
	}
}

// TestUsageMentionsCatalog: a command nobody can discover is a command
// nobody runs, and `--help` is where a person looks first.
func TestUsageMentionsCatalog(t *testing.T) {
	for _, want := range []string{"pkgx catalog update", "PKGX_CATALOG"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage does not mention %q", want)
		}
	}
}
