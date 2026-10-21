package app

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func withPlatforms(t *testing.T, plats []string, err error) {
	t.Helper()
	prev := fetchPlatforms
	fetchPlatforms = func() ([]string, error) { return plats, err }
	t.Cleanup(func() { fetchPlatforms = prev })
}

// ⛔ THE OUTPUT IS AN INPUT. Each line is handed straight back to
// `--platform`, by a shell loop in a CVE lane — so it carries nothing but
// the platforms: no header, no count, no decoration. A caller that has to
// strip anything is a caller that will strip the wrong thing one day.
func TestCatalogPlatformsPrintsOnlyPlatforms(t *testing.T) {
	withPlatforms(t, []string{
		"darwin/aarch64", "darwin/x86-64", "linux/aarch64",
		"linux/s390x", "linux/x86-64", "windows/x86-64",
	}, nil)
	var out, errb bytes.Buffer
	if code := runCatalogCmd([]string{"platforms"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	want := "darwin/aarch64\ndarwin/x86-64\nlinux/aarch64\nlinux/s390x\nlinux/x86-64\nwindows/x86-64\n"
	if out.String() != want {
		t.Errorf("stdout =\n%q\nwant\n%q", out.String(), want)
	}
	if errb.Len() != 0 {
		t.Errorf("stderr got %q — a caller reading stdout must get nothing else", errb.String())
	}
}

// ⛔ A FAILURE IS NOT AN EMPTY LIST. The caller is a loop: on exit 0 with no
// output it scans nothing and reports a clean fleet, which is the same
// sentence as "every platform is clean" and the opposite fact. bottle's
// Platforms refuses to return an empty list; this is the other half of that
// promise, here where the exit status is decided.
func TestCatalogPlatformsFailsLoudly(t *testing.T) {
	withPlatforms(t, nil, errors.New("the index has 2 entry(ies) and none of them names a platform"))
	var out, errb bytes.Buffer
	if code := runCatalogCmd([]string{"platforms"}, &out, &errb); code != 1 {
		t.Errorf("code=%d, want 1", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout got %q on a failure — a loop would consume it", out.String())
	}
	if !strings.Contains(errb.String(), "none of them names a platform") {
		t.Errorf("stderr does not carry the reason: %q", errb.String())
	}
}

// The verb is reachable and spelled as the usage says, and an unknown
// spelling is a usage error rather than a quiet success.
func TestCatalogPlatformsIsReachableAndTheUsageSaysSo(t *testing.T) {
	withPlatforms(t, []string{"linux/x86-64"}, nil)
	var out, errb bytes.Buffer
	if code := runCatalogCmd([]string{"platforms"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := runCatalogCmd([]string{"platform"}, &out, &errb); code != 2 {
		t.Errorf("a near-miss spelling gave %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "platforms") {
		t.Errorf("the usage does not name the verb: %q", errb.String())
	}
}
