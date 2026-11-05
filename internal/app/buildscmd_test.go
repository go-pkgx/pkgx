package app

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-zpkg/bottle"
)

// installBuild makes the directory an install makes: hashed, with a payload.
func installBuild(t *testing.T, store, project, version, seed string) string {
	t.Helper()
	v := bottle.Ver{Raw: version}
	hash := bottle.StoreHash(v, "digest:"+seed, "")
	if err := os.MkdirAll(filepath.Join(bottle.StoreBuildPrefix(store, project, v, hash), "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	return hash
}

// buildsStore points ZPKG_DIR at a scratch store and cuts the registry off,
// so these tests are about the join and the printing rather than the network.
func buildsStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ZPKG_DIR", dir)
	t.Setenv("ZPKG_CATALOG", "")
	// ⛔⛔ bottle.DistBase IS SET DIRECTLY, NOT THROUGH ZPKG_DIST. The
	// environment is read once by the app's configuration step, which these
	// tests do not run — so setting ZPKG_DIST here left DistBase at its
	// compiled-in default and the first version of this test went out to the
	// REAL ghcr registry. It passed for a while by asking the internet about
	// fftw.org, which is not what it claims to measure and is not available
	// to CI's offline jobs.
	//
	// A blackhole: connection refused immediately, so the registry half fails
	// fast and deterministically instead of waiting on DNS.
	old := bottle.DistBase
	bottle.DistBase = "oci://127.0.0.1:1/nothing"
	t.Cleanup(func() { bottle.DistBase = old })
	return dir
}

// ⛔⛔ THE WHOLE POINT: two builds of one version, both listed, each with the
// hash that names it. This is the answer to a refusal that could not be
// answered before — the resolver says "2 different builds and nothing here
// says which", and until now there was no way to see them.
func TestBuildsListsEveryBuildOfAVersion(t *testing.T) {
	store := buildsStore(t)
	a := installBuild(t, store, "fftw.org", "3.3.11", "a")
	b := installBuild(t, store, "fftw.org", "3.3.11", "b")
	if a == b {
		t.Fatal("the fixture made one build twice, so this would pass without listing two")
	}

	var out, errb bytes.Buffer
	code := runBuilds([]string{"fftw.org@3.3.11"}, &out, &errb)
	// The registry is unreachable on purpose, so the exit is 1 and the
	// reason is on stderr — but the installed half must still be printed.
	if code != 1 {
		t.Errorf("exit = %d, want 1 (the registry could not be asked)", code)
	}
	if !strings.Contains(errb.String(), "registry could not be asked") {
		t.Errorf("stderr does not say what failed: %q", errb.String())
	}
	for _, h := range []string{a, b} {
		if !strings.Contains(out.String(), h) {
			t.Errorf("build %s is not listed:\n%s", h, out.String())
		}
	}
	// ⛔ "?" AND NOT "no". A registry we could not reach has told us nothing,
	// and a column saying "no" would turn that into "not published" — which
	// is how a user concludes a build does not exist and rebuilds it.
	if !strings.Contains(out.String(), "PUBLISHED?") || !strings.Contains(out.String(), "?") {
		t.Errorf("the unknown registry half is not marked unknown:\n%s", out.String())
	}
	if strings.Contains(out.String(), "\tno\t") {
		t.Errorf("an unobtained answer was printed as a negative one:\n%s", out.String())
	}
}

// goSpec is two builds of one version that differ ONLY in the Go toolchain
// they were compiled with — the CVE rebuild case.
func goSpec(goVer, goHash string) bottle.Concrete {
	return bottle.Concrete{
		Platform: "linux/x86-64",
		Recipe:   "sha256:0123456789abcdef",
		Deps: []bottle.Dep{
			{Project: "go.dev", Version: goVer, Hash: goHash},
			{Project: "zlib.net", Version: "1.3.2", Hash: "cccccccccccccccc"},
		},
	}
}

// ⛔⛔ THE CASE THE WHOLE DESIGN IS FOR, RENDERED. A Go patch release fixes a
// standard-library defect: the recipe does not change, the upstream version
// does not change, only `go.dev` in the closure — so the fix is another build
// of the SAME version, and the table has to make the two tellable apart at a
// glance. A reader who cannot do that is back to comparing hashes.
func TestTheTableTellsTwoToolchainRebuildsApart(t *testing.T) {
	const bad, good = "27o3ysxva6swayci", "rnp44isdgdtzxcit"
	builds := []bottle.Build{
		{Version: bottle.Ver{Raw: "3.3.11", Variant: bad}, Installed: "/s", Published: true,
			Spec: goSpec("1.27.3", "aaaaaaaaaaaaaaaa")},
		{Version: bottle.Ver{Raw: "3.3.11", Variant: good}, Published: true,
			Spec: goSpec("1.27.4", "bbbbbbbbbbbbbbbb")},
	}

	var out bytes.Buffer
	printBuilds(&out, "fftw.org", builds, false)
	got := out.String()
	t.Logf("\n%s", got)

	if !strings.Contains(got, "go.dev 1.27.3") || !strings.Contains(got, "go.dev 1.27.4") {
		t.Errorf("the toolchain that distinguishes them is not shown:\n%s", got)
	}
	// ⛔ AND THE DEPENDENCY THEY SHARE IS NOT. Without this the column grows
	// into the whole closure and stops being readable, which is the failure
	// mode it was written to avoid.
	if strings.Contains(got, "zlib.net") {
		t.Errorf("a dependency both builds share was printed:\n%s", got)
	}
}

// And with one of them withdrawn, the row says so FIRST and the note under
// the table says the build still resolves.
func TestAWithdrawnBuildIsMarkedInTheRowAndExplainedBelow(t *testing.T) {
	const bad, good = "27o3ysxva6swayci", "rnp44isdgdtzxcit"
	oldFor := bottleWithdrawalOf
	bottleWithdrawalOf = func(project, version, hash string) (bottle.Withdrawal, bool) {
		if hash == bad {
			return bottle.Withdrawal{Version: version, Hash: bad, Because: "CVE-2026-1234", SupersededBy: good}, true
		}
		return bottle.Withdrawal{}, false
	}
	t.Cleanup(func() { bottleWithdrawalOf = oldFor })

	builds := []bottle.Build{
		{Version: bottle.Ver{Raw: "3.3.11", Variant: bad}, Published: true, Spec: goSpec("1.27.3", "aaaaaaaaaaaaaaaa")},
		{Version: bottle.Ver{Raw: "3.3.11", Variant: good}, Published: true, Spec: goSpec("1.27.4", "bbbbbbbbbbbbbbbb")},
	}
	var out bytes.Buffer
	printBuilds(&out, "fftw.org", builds, false)
	got := out.String()
	t.Logf("\n%s", got)

	line := rowFor(t, got, bad)
	if !strings.Contains(line, "CVE-2026-1234") {
		t.Errorf("the withdrawn row does not name the advisory: %q", line)
	}
	// ⛔ THE WARNING COMES BEFORE THE DESCRIPTION. A reader scanning to choose
	// must not have to read a dependency list first to learn this is the one
	// a CVE caught.
	if i, j := strings.Index(line, "CVE-2026-1234"), strings.Index(line, "go.dev"); i < 0 || j < 0 || i > j {
		t.Errorf("the warning is not first in the cell: %q", line)
	}
	if !strings.Contains(line, "rnp44i") {
		t.Errorf("the row does not say what replaces it: %q", line)
	}
	if rowFor(t, got, good) == "" || strings.Contains(rowFor(t, got, good), "CVE") {
		t.Errorf("the clean sibling was marked:\n%s", got)
	}
}

// rowFor returns the table line holding a hash.
func rowFor(t *testing.T, out, hash string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, hash) {
			return l
		}
	}
	return ""
}

// A single build has nothing to be told apart from, so the row carries a
// summary of what was recorded rather than an empty cell.
func TestASingleBuildIsSummarisedRatherThanLeftBlank(t *testing.T) {
	var out bytes.Buffer
	printBuilds(&out, "fftw.org", []bottle.Build{{
		Version:   bottle.Ver{Raw: "3.3.11", Variant: "27o3ysxva6swayci"},
		Published: true, Spec: goSpec("1.27.3", "aaaaaaaaaaaaaaaa"),
	}}, false)
	got := out.String()
	for _, want := range []string{"linux/x86-64", "2 dependencies"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in:\n%s", want, got)
		}
	}
}

// ⛔ A BUILD NOTHING RECORDS SAYS SO. An empty cell reads as a rendering
// fault; "nothing recorded" is the truth, and it is the state of every bottle
// in the registry today.
func TestABuildWithNoSpecSaysNothingWasRecorded(t *testing.T) {
	var out bytes.Buffer
	printBuilds(&out, "fftw.org", []bottle.Build{
		{Version: bottle.Ver{Raw: "3.3.11"}, Published: true},
		{Version: bottle.Ver{Raw: "3.3.11", Variant: "27o3ysxva6swayci"}, Published: true,
			Spec: goSpec("1.27.3", "aaaaaaaaaaaaaaaa")},
	}, false)
	got := out.String()
	if !strings.Contains(got, "nothing recorded") {
		t.Errorf("a build with no spec got a blank cell:\n%s", got)
	}
}

// ⛔⛔ A PUBLISHED BOTTLE WITH NO HASH IS WHAT THE REGISTRY IS FULL OF TODAY,
// and it must print as a fact about the bottle rather than as a blank cell.
// Found by accident: the first version of this file set ZPKG_DIST, which the
// app reads only through its configuration step, so the test went out to the
// real ghcr and came back with `fftw.org 3.3.11` carrying no hash — a row
// whose HASH column was empty and which no reader could name or explain.
func TestABottleWithNoIdentityIsNamedRatherThanBlank(t *testing.T) {
	var out bytes.Buffer
	printBuilds(&out, "fftw.org", []bottle.Build{
		{Version: bottle.Ver{Raw: "3.3.11"}, Published: true},
	}, false)
	if !strings.Contains(out.String(), "(none)") {
		t.Errorf("a hashless bottle printed as a blank cell:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "cannot be named by hash") {
		t.Errorf("nothing explains what (none) means:\n%s", out.String())
	}
	// And the note appears only when there IS such a row.
	var out2 bytes.Buffer
	printBuilds(&out2, "fftw.org", []bottle.Build{
		{Version: bottle.Ver{Raw: "3.3.11", Variant: "abcdefghijklmnop"}, Published: true},
	}, false)
	if strings.Contains(out2.String(), "(none)") {
		t.Errorf("the note was printed with nothing to explain:\n%s", out2.String())
	}
}

// Only the asked-for version. A store holding two versions must not answer
// about both: builds are the siblings of ONE version.
func TestBuildsAnswersAboutTheVersionAsked(t *testing.T) {
	store := buildsStore(t)
	want := installBuild(t, store, "fftw.org", "3.3.11", "a")
	other := installBuild(t, store, "fftw.org", "3.3.10", "a")

	var out, errb bytes.Buffer
	runBuilds([]string{"fftw.org@3.3.11"}, &out, &errb)
	if !strings.Contains(out.String(), want) {
		t.Errorf("the asked-for build is missing:\n%s", out.String())
	}
	if other != want && strings.Contains(out.String(), other) {
		t.Errorf("a build of another version was listed:\n%s", out.String())
	}
}

// ⛔ A VERSION IS REQUIRED, AND THE MESSAGE SAYS WHY. "builds of fftw.org" is
// not a question this answers: builds are siblings within a version.
func TestBuildsRefusesASpecWithNoVersion(t *testing.T) {
	buildsStore(t)
	for _, arg := range []string{"fftw.org", "fftw.org@", "@3.3.11"} {
		var out, errb bytes.Buffer
		if code := runBuilds([]string{arg}, &out, &errb); code != 2 {
			t.Errorf("%q: exit = %d, want 2", arg, code)
		}
		if !strings.Contains(errb.String(), "siblings") {
			t.Errorf("%q: the refusal does not say why: %q", arg, errb.String())
		}
	}
}

// A store with nothing in it and a registry that answers says so plainly,
// rather than printing an empty table.
func TestBuildsSaysWhenThereAreNone(t *testing.T) {
	buildsStore(t)
	var out, errb bytes.Buffer
	// The registry is a blackhole here, so the "none" line is suppressed and
	// the failure is reported instead — stating "none" would assert as fact
	// the very thing we just said we could not learn.
	if code := runBuilds([]string{"nothing.org@1.0.0"}, &out, &errb); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if strings.Contains(out.String(), "no build of") {
		t.Errorf("it claimed there are none while the registry was unknown:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "registry could not be asked") {
		t.Errorf("stderr = %q", errb.String())
	}
}

// A registry that ANSWERS and carries nothing is a different outcome from a
// registry that could not be asked: this one is allowed to say "none", and
// exits 0 because nothing went wrong.
func TestBuildsSaysNoneWhenTheRegistryAnswersWithNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZPKG_DIR", dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The versions list, empty. Not a 404: that is "could not be asked".
		fmt.Fprint(w, "")
	}))
	t.Cleanup(srv.Close)
	old := bottle.DistBase
	bottle.DistBase = srv.URL
	t.Cleanup(func() { bottle.DistBase = old })

	var out, errb bytes.Buffer
	// --platform is given in its VALID form here, which is also the only
	// place that branch is exercised.
	code := runBuilds([]string{"--platform", "linux/x86-64", "nothing.org@1.0.0"}, &out, &errb)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (the registry answered; it simply has none): %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "no build of nothing.org 1.0.0") {
		t.Errorf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "linux/x86-64") {
		t.Errorf("the answer does not name the platform it is about: %q", out.String())
	}
}

func TestBuildsRejectsABadPlatform(t *testing.T) {
	buildsStore(t)
	var out, errb bytes.Buffer
	if code := runBuilds([]string{"--platform", "linux", "fftw.org@3.3.11"}, &out, &errb); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "is not os/arch") {
		t.Errorf("stderr = %q", errb.String())
	}
	var out2, errb2 bytes.Buffer
	if code := runBuilds([]string{"too", "many"}, &out2, &errb2); code != 2 {
		t.Errorf("two args: exit = %d, want 2", code)
	}
	var out3, errb3 bytes.Buffer
	if code := runBuilds([]string{"--nope"}, &out3, &errb3); code != 2 {
		t.Errorf("bad flag: exit = %d, want 2", code)
	}
}

// ⛔⛔ NAMING A BUILD MUST NOT SILENTLY RUN A DIFFERENT ONE. splitSpec cuts
// the hash off so the version still parses; without this refusal,
// `fftw.org@3.3.11/7k2mq4` would resolve to SOME build of 3.3.11 and look
// like it had honoured the request.
func TestASpecNamingABuildIsRefusedRatherThanIgnored(t *testing.T) {
	m := &commandMapper{}
	_, _, err := m.resolve("fftw.org@3.3.11/7k2mq4")
	if err == nil {
		t.Fatal("a build-by-hash spec resolved as if the hash were not there")
	}
	for _, want := range []string{"7k2mq4", "zpkgs builds", "fftw.org@3.3.11"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not contain %q: %v", want, err)
		}
	}
}

// ⛔ AND A PROJECT NAME WITH A SLASH IS NOT A BUILD REFERENCE. A third of the
// pantry is named this way; reading `x.org/pciaccess` as a hash reference
// would break them all.
func TestASlashedProjectNameIsNotABuildReference(t *testing.T) {
	for _, spec := range []string{
		"x.org/pciaccess", "gnome.org/libxml2", "github.com/gabime/spdlog",
	} {
		if h := buildHashOf(spec); h != "" {
			t.Errorf("%q read as naming build %q", spec, h)
		}
		if p, _ := splitSpec(spec); p != spec {
			t.Errorf("splitSpec(%q) lost part of the project name: %q", spec, p)
		}
	}
}
