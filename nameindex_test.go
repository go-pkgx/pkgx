package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

// indexStore lays out a store with a catalogue, and optionally its index,
// exactly where pkgx looks for them — and points PKGX_DIR at it.
func indexStore(t *testing.T, cat bottle.Catalog, withIndex bool) (dir, jsonPath, namesPath string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	t.Setenv("PKGX_CATALOG", "")
	osn, arch := bottle.HostSlug()
	jsonPath, namesPath = catalogPath(osn, arch), catalogNamesPath(osn, arch)
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := bottle.MarshalCatalog(cat)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if withIndex {
		if err := writeNameIndex(namesPath, cat, int64(len(b))); err != nil {
			t.Fatal(err)
		}
	}
	return dir, jsonPath, namesPath
}

func threeProjects() bottle.Catalog {
	return bottle.Catalog{Projects: []bottle.CatalogProject{
		{Project: "gnu.org/bash", Versions: []string{"5.3"}, Deps: []string{"gnu.org/ncurses"}},
		{Project: "curl.se", Versions: []string{"8.20"}, Summary: "transfer a URL"},
		// No version: "named by the pantry, nothing published", which is
		// what `no bottle here` is made of.
		{Project: "aerc-mail.org"},
	}}
}

func TestNameIndexRoundTrip(t *testing.T) {
	_, _, names := indexStore(t, threeProjects(), true)
	got, ok := readNameIndex(names, mustJSONPath(t))
	if !ok {
		t.Fatal("an index just written was refused")
	}
	if len(got.Projects) != 3 {
		t.Fatalf("projects = %+v", got.Projects)
	}
	// SORTED, so the file is stable between runs over the same catalogue.
	var order []string
	for _, p := range got.Projects {
		order = append(order, p.Project)
	}
	want := []string{"aerc-mail.org", "curl.se", "gnu.org/bash"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", order, want)
	}
	// The version survives, and so does its ABSENCE — the two are different
	// facts and lsNote prints different words for them.
	for _, p := range got.Projects {
		switch p.Project {
		case "gnu.org/bash":
			if len(p.Versions) != 1 || p.Versions[0] != "5.3" {
				t.Errorf("bash versions = %v", p.Versions)
			}
		case "aerc-mail.org":
			if len(p.Versions) != 0 {
				t.Errorf("a project with no version came back with %v", p.Versions)
			}
		}
	}
}

// The index carries NO dependencies and no summaries, which is why it is a
// fraction of the catalogue. Asserted so nobody "improves" it into a second
// copy of the catalogue.
func TestNameIndexCarriesOnlyWhatCompletionShows(t *testing.T) {
	_, _, names := indexStore(t, threeProjects(), true)
	b, err := os.ReadFile(names)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"gnu.org/ncurses", "transfer a URL"} {
		if bytes.Contains(b, []byte(unwanted)) {
			t.Errorf("the index carries %q, which completion never shows:\n%s", unwanted, b)
		}
	}
	got, _ := readNameIndex(names, mustJSONPath(t))
	for _, p := range got.Projects {
		if len(p.Deps) > 0 || p.Summary != "" {
			t.Errorf("%s came back with deps/summary: %+v", p.Project, p)
		}
	}
}

// ⛔ EVERY DOUBT IS A REFUSAL, and a refusal means "read the JSON instead".
func TestNameIndexRefusals(t *testing.T) {
	cases := []struct {
		name string
		// mutate edits the index (and may edit the catalogue) after both
		// have been written correctly.
		mutate func(t *testing.T, jsonPath, names string)
	}{
		{"no index at all", func(t *testing.T, _, names string) {
			if err := os.Remove(names); err != nil {
				t.Fatal(err)
			}
		}},
		{"an empty file", func(t *testing.T, _, names string) {
			writeIndexFile(t, names, "")
		}},
		{"no header", func(t *testing.T, _, names string) {
			writeIndexFile(t, names, "curl.se\t8.20\n")
		}},
		{
			// ⛔ OTHERWISE VALID. The first version of this case wrote a
			// made-up size (100) as well as the wrong magic, so the SIZE
			// check rejected it — and deleting the magic check left the test
			// green. A case aimed at one guard has to satisfy every other
			// guard, or it is a test for whichever guard happens to fire
			// first.
			"the wrong magic, with a correct size and count",
			func(t *testing.T, jsonPath, names string) {
				writeIndexFile(t, names, fmt.Sprintf("#something-else %d %d 3\n%s",
					nameIndexVersion, jsonSize(t, jsonPath), threeLines()))
			},
		},
		{"too few header fields", func(t *testing.T, _, names string) {
			writeIndexFile(t, names, nameIndexMagic+" 1 100\ncurl.se\t8.20\n")
		}},
		{
			// Otherwise valid, for the reason the magic case above gives: a
			// made-up size here meant the SIZE guard rejected it and
			// deleting the version check left the test green.
			"a format version this pkgx does not know, otherwise valid",
			func(t *testing.T, jsonPath, names string) {
				writeIndexFile(t, names, fmt.Sprintf("%s %d %d 3\n%s",
					nameIndexMagic, nameIndexVersion+1, jsonSize(t, jsonPath), threeLines()))
			},
		},
		{"a header size that is not a number", func(t *testing.T, _, names string) {
			writeIndexFile(t, names, nameIndexMagic+" 1 lots 1\ncurl.se\t8.20\n")
		}},
		{"a zero size", func(t *testing.T, _, names string) {
			writeIndexFile(t, names, nameIndexMagic+" 1 0 1\ncurl.se\t8.20\n")
		}},
		{"a count that is not a number", func(t *testing.T, _, names string) {
			writeIndexFile(t, names, nameIndexMagic+" 1 100 many\ncurl.se\t8.20\n")
		}},
		{"a negative count", func(t *testing.T, _, names string) {
			writeIndexFile(t, names, nameIndexMagic+" 1 100 -1\ncurl.se\t8.20\n")
		}},
		{
			// ⛔ THE CASE THE FORMAT EXISTS FOR: the catalogue was refreshed
			// and the index was not.
			"a catalogue that changed size",
			func(t *testing.T, jsonPath, _ string) {
				b, err := os.ReadFile(jsonPath)
				if err != nil {
					t.Fatal(err)
				}
				// Valid JSON, different length: a reader that only checked
				// "is it parseable" would accept the stale index.
				if err := os.WriteFile(jsonPath, append(b, ' '), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{"no catalogue beside it", func(t *testing.T, jsonPath, _ string) {
			if err := os.Remove(jsonPath); err != nil {
				t.Fatal(err)
			}
		}},
		{
			// A SHORT index is the dangerous direction: completion would
			// quietly stop offering whatever fell off the end.
			"fewer lines than the header claims",
			func(t *testing.T, _, names string) {
				b, err := os.ReadFile(names)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.SplitAfter(string(b), "\n")
				writeIndexFile(t, names, strings.Join(lines[:len(lines)-2], ""))
			},
		},
		{"a line with no name", func(t *testing.T, _, names string) {
			b, err := os.ReadFile(names)
			if err != nil {
				t.Fatal(err)
			}
			writeIndexFile(t, names, strings.Replace(string(b), "curl.se\t", "\t", 1))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, jsonPath, names := indexStore(t, threeProjects(), true)
			// It was usable before the edit, or the case proves nothing.
			if _, ok := readNameIndex(names, jsonPath); !ok {
				t.Fatal("the index was already unusable before the edit")
			}
			c.mutate(t, jsonPath, names)
			if _, ok := readNameIndex(names, jsonPath); ok {
				t.Error("accepted")
			}
		})
	}
}

// completionCatalog prefers the index, and falls back to the catalogue — with
// the SAME answer either way, which is the only thing that makes the fast path
// safe to take.
func TestCompletionCatalogPrefersTheIndexAndAgreesWithTheJSON(t *testing.T) {
	dir, _, names := indexStore(t, threeProjects(), true)
	withIndex := completionCatalog(dir)
	if len(withIndex.Projects) != 3 {
		t.Fatalf("with the index: %+v", withIndex.Projects)
	}
	if err := os.Remove(names); err != nil {
		t.Fatal(err)
	}
	withoutIndex := completionCatalog(dir)
	if len(withoutIndex.Projects) != 3 {
		t.Fatalf("without the index: %+v", withoutIndex.Projects)
	}
	// ⛔ The same completions, not merely the same count. This is the whole
	// contract: the index may make completion faster and must not make it
	// answer differently.
	for _, word := range []string{"", "gnu", "gnu.org/", "curl", "aerc", "nope"} {
		a, b := completionStrings(withIndex, word), completionStrings(withoutIndex, word)
		if a != b {
			t.Errorf("completing %q: index gave %q, catalogue gave %q", word, a, b)
		}
	}
}

// A named PKGX_CATALOG wins outright: there is no index for a file somebody
// pointed at, and answering from one built beside a DIFFERENT catalogue is the
// exact disagreement this format is careful about.
func TestCompletionCatalogIgnoresTheIndexWhenAFileIsNamed(t *testing.T) {
	dir, _, _ := indexStore(t, threeProjects(), true)
	other := bottle.Catalog{Projects: []bottle.CatalogProject{{Project: "only.this", Versions: []string{"1.0"}}}}
	b, err := bottle.MarshalCatalog(other)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "named.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PKGX_CATALOG", p)
	got := completionCatalog(dir)
	if len(got.Projects) != 1 || got.Projects[0].Project != "only.this" {
		t.Errorf("the named catalogue was ignored in favour of the index: %+v", got.Projects)
	}
}

func completionStrings(cat bottle.Catalog, word string) string {
	var out []string
	for _, n := range cat.Complete(word) {
		out = append(out, fmt.Sprintf("%s|%v|%d", n.Name, n.Leaf, n.Under))
	}
	return strings.Join(out, " ")
}

func mustJSONPath(t *testing.T) string {
	t.Helper()
	osn, arch := bottle.HostSlug()
	return catalogPath(osn, arch)
}

func writeIndexFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jsonSize is the catalogue's real byte length, so a refusal case can satisfy
// the size guard and still exercise the one it is aimed at.
func jsonSize(t *testing.T, jsonPath string) int64 {
	t.Helper()
	st, err := os.Stat(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

// threeLines is the body of a valid index over threeProjects(), so a case can
// keep the COUNT honest while breaking something else.
func threeLines() string {
	return "aerc-mail.org\t\ncurl.se\t8.20\ngnu.org/bash\t5.3\n"
}

// `pkgx catalog reindex` builds the index from the catalogue already on disk,
// with no network.
//
// It exists for the air-gapped case catalogPath's comment describes — an image
// that SHIPS a catalogue has no index, so until this its completion took the
// slow path in exactly the environment that can least afford it.
func TestCatalogReindexBuildsTheIndexOffline(t *testing.T) {
	_, jsonPath, names := indexStore(t, threeProjects(), false)
	if _, err := os.Stat(names); err == nil {
		t.Fatal("the store already had an index, so this proves nothing")
	}
	var out, errb bytes.Buffer
	if code := runCatalogCmd([]string{"reindex"}, &out, &errb); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	cat, ok := readNameIndex(names, jsonPath)
	if !ok {
		t.Fatal("reindex wrote an index its own reader refuses")
	}
	if len(cat.Projects) != 3 {
		t.Errorf("projects = %+v", cat.Projects)
	}
	// It says where it landed, because an air-gapped image has to know the
	// path to put the file at, and reading it out of the program beats
	// restating it by hand.
	if !strings.Contains(out.String(), names) || !strings.Contains(out.String(), "3 name(s)") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestCatalogReindexRefusals(t *testing.T) {
	t.Run("no catalogue to index", func(t *testing.T) {
		t.Setenv("PKGX_DIR", t.TempDir())
		t.Setenv("PKGX_CATALOG", "")
		var out, errb bytes.Buffer
		if code := runCatalogCmd([]string{"reindex"}, &out, &errb); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		// The command that fixes it, which is the only useful thing to say.
		if !strings.Contains(errb.String(), "pkgx catalog update") {
			t.Errorf("stderr = %q", errb.String())
		}
	})
	t.Run("a catalogue that does not parse", func(t *testing.T) {
		_, jsonPath, _ := indexStore(t, threeProjects(), false)
		writeIndexFile(t, jsonPath, "{not json")
		var out, errb bytes.Buffer
		if code := runCatalogCmd([]string{"reindex"}, &out, &errb); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "reindex") {
			t.Errorf("stderr = %q", errb.String())
		}
	})
	t.Run("an unknown subcommand", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := runCatalogCmd([]string{"nonesuch"}, &out, &errb); code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
		// The usage line names BOTH subcommands, or reindex is a feature
		// nobody can find.
		if !strings.Contains(errb.String(), "update|reindex") {
			t.Errorf("stderr = %q", errb.String())
		}
	})
}

// ⛔ reindex must use the length of what it READ, not a fresh Stat. Between
// the read and the write the catalogue could be replaced, and an index whose
// header described a catalogue it was not built from is the one thing the
// format exists to prevent. Checked by its effect: the header's size matches
// the bytes that were parsed.
func TestCatalogReindexRecordsTheSizeItRead(t *testing.T) {
	_, jsonPath, names := indexStore(t, threeProjects(), false)
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if code := runCatalogCmd([]string{"reindex"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("reindex failed")
	}
	idx, err := os.ReadFile(names)
	if err != nil {
		t.Fatal(err)
	}
	header, _, _ := strings.Cut(string(idx), "\n")
	gotBytes, gotCount, ok := parseNameIndexHeader(header)
	if !ok {
		t.Fatalf("header = %q", header)
	}
	if gotBytes != int64(len(b)) {
		t.Errorf("header size = %d, want the %d bytes that were read", gotBytes, len(b))
	}
	if gotCount != 3 {
		t.Errorf("header count = %d, want 3", gotCount)
	}
}

// errAfterHeader hands back a valid header and then fails, which is the one
// shape a real file cannot be made to have.
type errAfterHeader struct {
	header string
	done   bool
}

func (e *errAfterHeader) Read(p []byte) (int, error) {
	if e.done {
		return 0, fmt.Errorf("the disk went away")
	}
	e.done = true
	n := copy(p, e.header)
	return n, nil
}

// ⛔ A MID-READ FAILURE IS A REFUSAL, not a short catalogue. If it fell through
// to the count check it would still refuse — but by accident, and a reader
// that refuses for the wrong reason is one line away from refusing for none.
func TestNameIndexRefusesAMidReadFailure(t *testing.T) {
	r := &errAfterHeader{header: fmt.Sprintf("%s %d 1234 3\n", nameIndexMagic, nameIndexVersion)}
	if _, ok := parseNameIndex(r, 1234); ok {
		t.Error("accepted an index whose body could not be read")
	}
	// And the control: the same header with a readable (empty) body is
	// refused for the COUNT, so the case above is not passing because every
	// input is refused.
	if _, ok := parseNameIndex(strings.NewReader(fmt.Sprintf("%s %d 1234 3\n", nameIndexMagic, nameIndexVersion)), 1234); ok {
		t.Error("accepted an index with three promised names and none present")
	}
	// And a complete one IS accepted, through the same function.
	good := fmt.Sprintf("%s %d 1234 3\n%s", nameIndexMagic, nameIndexVersion, threeLines())
	if _, ok := parseNameIndex(strings.NewReader(good), 1234); !ok {
		t.Error("refused a complete index, so the refusals above prove nothing")
	}
}

// failNamesWrite makes writing the INDEX fail and leaves every other write
// alone, so the two commands' opposite promises about that failure can be
// checked.
func failNamesWrite(t *testing.T) {
	t.Helper()
	prev := osWriteFile
	osWriteFile = func(name string, data []byte, perm os.FileMode) error {
		if strings.HasSuffix(name, ".names") {
			return fmt.Errorf("no room for the index")
		}
		return prev(name, data, perm)
	}
	t.Cleanup(func() { osWriteFile = prev })
}

// ⛔ THE TWO COMMANDS PROMISE OPPOSITE THINGS ABOUT THE SAME FAILURE, and that
// is deliberate:
//
//	catalog update   WARNS and succeeds — the catalogue is refreshed, which is
//	                 what was asked for, and completion falls back to reading
//	                 it. Failing here would turn a performance file into a
//	                 reason the catalogue does not refresh.
//	catalog reindex  FAILS — writing the index is the whole job, and a command
//	                 that reports success having done nothing is worse than
//	                 one that reports failure.
func TestAnUnwritableIndexWarnsOnUpdateAndFailsOnReindex(t *testing.T) {
	t.Run("update warns and still refreshes the catalogue", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PKGX_DIR", dir)
		t.Setenv("PKGX_CATALOG", "")
		withFetch(t, threeProjects(), nil)
		failNamesWrite(t)
		var out, errb bytes.Buffer
		if code := runCatalogCmd([]string{"update"}, &out, &errb); code != 0 {
			t.Fatalf("code = %d, want 0 — the catalogue refresh must not fail on the index", code)
		}
		if !strings.Contains(errb.String(), "completion will read the catalogue instead") {
			t.Errorf("stderr = %q, want the warning and what it means", errb.String())
		}
		// The catalogue really is there: the command did its actual job.
		osn, arch := bottle.HostSlug()
		if _, err := os.Stat(catalogPath(osn, arch)); err != nil {
			t.Errorf("the catalogue was not written: %v", err)
		}
		if !strings.Contains(out.String(), "3 project(s)") {
			t.Errorf("stdout = %q", out.String())
		}
		// And it does NOT claim an index it failed to write.
		if strings.Contains(out.String(), "tab-completion reads") {
			t.Errorf("stdout claims an index that was not written: %q", out.String())
		}
	})
	t.Run("reindex fails", func(t *testing.T) {
		indexStore(t, threeProjects(), false)
		failNamesWrite(t)
		var out, errb bytes.Buffer
		if code := runCatalogCmd([]string{"reindex"}, &out, &errb); code != 1 {
			t.Fatalf("code = %d, want 1 — writing the index is the whole job", code)
		}
		if !strings.Contains(errb.String(), "no room for the index") {
			t.Errorf("stderr = %q", errb.String())
		}
	})
}

// ⛔ THE INDEX IS A SECOND READER OF THE SAME DATA AND MUST APPLY THE SAME
// CHECKS.
//
// Demonstrated 2026-10-11, before readNameIndex validated names: an index
// naming `../outside` completed to
//
//	../outside	1.0  ✓ 9.9.9
//
// and that `✓ 9.9.9` is a directory OUTSIDE $PKGX_DIR, reported as installed —
// because lsNote asks bottle.InstalledVersions, which does
// filepath.Join(dir, project). bottle.UnmarshalCatalog refuses the name
// outright; this reader built a Catalog by hand and skipped the refusal.
//
// The test is a PAIR: the traversing name is refused, and a real nested
// project name (`x.org/protocol/xcb`, which contains slashes and must stay
// legal) is accepted — a validator that refused everything would pass the
// first half and break the tool.
func TestTheIndexRefusesANameTheCatalogueParserWouldRefuse(t *testing.T) {
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "v9.9.9"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("a traversing name is refused", func(t *testing.T) {
		_, jsonPath, names := indexStore(t, threeProjects(), true)
		body := fmt.Sprintf("%s %d %d 1\n../%s\t1.0\n",
			nameIndexMagic, nameIndexVersion, jsonSize(t, jsonPath), filepath.Base(outside))
		writeIndexFile(t, names, body)
		if _, ok := readNameIndex(names, jsonPath); ok {
			t.Fatal("accepted an index naming a path outside the store")
		}
		// And the same name through the CATALOGUE parser, so the two readers
		// are shown to agree rather than assumed to.
		bad := []byte(`{"generated":"2026-10-11T00:00:00Z","projects":[{"project":"../x","versions":["1.0"]}]}`)
		if _, err := bottle.UnmarshalCatalog(bad); err == nil {
			t.Error("the catalogue parser accepts it too — then this guard is in the wrong place")
		}
	})

	t.Run("a real nested project name is accepted", func(t *testing.T) {
		cat := bottle.Catalog{Projects: []bottle.CatalogProject{
			{Project: "x.org/protocol/xcb", Versions: []string{"1.17.0"}},
		}}
		_, jsonPath, names := indexStore(t, cat, true)
		got, ok := readNameIndex(names, jsonPath)
		if !ok {
			t.Fatal("refused a legitimate nested project name")
		}
		if len(got.Projects) != 1 || got.Projects[0].Project != "x.org/protocol/xcb" {
			t.Errorf("projects = %+v", got.Projects)
		}
	})
}
