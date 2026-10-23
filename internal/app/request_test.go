package app

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/errcode"
)

type fakeOCI struct {
	tags  []string
	plats []string
	err   error
	perr  error
}

func (f fakeOCI) ListTags(string) ([]string, error)          { return f.tags, f.err }
func (f fakeOCI) Platforms(string, string) ([]string, error) { return f.plats, f.perr }

func withOCI(t *testing.T, f fakeOCI) {
	t.Helper()
	prev := newOCI
	newOCI = func() (ociAsker, error) { return f, nil }
	t.Cleanup(func() { newOCI = prev })
}

// ⛔ THE PROMISE THIS COMMAND MAKES IS ABOUT WHAT IT DOES *NOT* CARRY, so
// that is the test. The line that prompted the whole feature was
//
//	pkgx openstack user list --domain stratuslab
//
// and a reporter that attached "the command that failed" would publish an
// organisation's infrastructure names into a public issue tracker, from a
// machine whose owner was debugging rather than publishing.
func TestTheReportCarriesNothingItWasNotGiven(t *testing.T) {
	withOCI(t, fakeOCI{})
	t.Setenv("PKGX_SECRET_LOOKING_THING", "stratuslab-token")
	var out, errb bytes.Buffer
	if code := runRequest([]string{"--url", "opendev.org/openstack/python-openstackclient"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	text := out.String()
	// Everything the process knows and must not say.
	for _, forbidden := range []string{
		"stratuslab",  // the user's domain, from their argv
		"--domain",    // any argv at all
		"PKGX_SECRET", // the environment
		"/Users/",     // a path, which carries the account name
		"/home/",      //
		"token",       //
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the report carries %q:\n%s", forbidden, text)
		}
	}
	// And the URL is built from the same text, so it cannot smuggle more.
	u := strings.SplitN(text, "https://", 2)
	if len(u) != 2 {
		t.Fatalf("--url printed no link:\n%s", text)
	}
	link, err := url.Parse("https://" + strings.TrimSpace(u[1]))
	if err != nil {
		t.Fatalf("the link does not parse: %v", err)
	}
	body := link.Query().Get("body")
	for _, forbidden := range []string{"stratuslab", "--domain", "PKGX_SECRET", "/Users/", "/home/"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the issue body carries %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "opendev.org/openstack/python-openstackclient") {
		t.Errorf("the issue body does not name the project: %s", body)
	}
}

// Nothing is sent. The default run prints the report and says so, and there
// is no flag that makes it send: the only path to GitHub is a person opening
// a link they have read.
func TestItSendsNothingAndSaysSo(t *testing.T) {
	withOCI(t, fakeOCI{})
	var out, errb bytes.Buffer
	if code := runRequest([]string{"some.org/thing"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Nothing was sent") {
		t.Errorf("the default run does not say that nothing was sent:\n%s", out.String())
	}
	if strings.Contains(out.String(), "https://") {
		t.Errorf("the default run printed a link without being asked:\n%s", out.String())
	}
}

// The three states a reader needs told apart, because "it is missing" covers
// two very different requests: build it at all, or build it for me.
func TestTheThreeStates(t *testing.T) {
	host := hostPlatform()
	for _, c := range []struct {
		name string
		oci  fakeOCI
		want string
	}{
		{"never published", fakeOCI{}, "never published in this registry"},
		// ⛔ The shape that fooled the first live run: ghcr has no referrers
		// API, so attestations are tagged `sha256-<digest>` and sit in the
		// same listing. Two of those are not two versions.
		{"only referrer tags", fakeOCI{tags: []string{
			"sha256-1b6d86777009e1e502260a9408da230cfc8a5e6d6fd6f8a339d7b77348e6c330",
			"sha256-5e3e8dd10513699116134fe1455e53db8d7d6b575a25d539bac524fc920c12b9",
		}}, "never published in this registry"},
		{"not for this platform", fakeOCI{tags: []string{"1.0.0"}, plats: []string{"linux/s390x"}}, "NOT for " + host},
		{"published here", fakeOCI{tags: []string{"1.0.0"}, plats: []string{host}}, "published for this platform"},
	} {
		t.Run(c.name, func(t *testing.T) {
			withOCI(t, c.oci)
			var out, errb bytes.Buffer
			runRequest([]string{"some.org/thing"}, &out, &errb)
			if !strings.Contains(out.String(), c.want) {
				t.Errorf("state not reported as %q:\n%s", c.want, out.String())
			}
		})
	}
}

// ⛔ A REQUEST FOR SOMETHING THAT IS ALREADY HERE IS REFUSED. Filing it would
// put an issue with a false premise in front of a maintainer, and the next
// one would be read with less trust.
func TestItRefusesToRequestWhatIsAlreadyPublished(t *testing.T) {
	withOCI(t, fakeOCI{tags: []string{"1.0.0"}, plats: []string{hostPlatform()}})
	var out, errb bytes.Buffer
	if code := runRequest([]string{"--url", "some.org/thing"}, &out, &errb); code != 1 {
		t.Errorf("code=%d, want 1", code)
	}
	if strings.Contains(out.String(), "https://") {
		t.Errorf("it offered to file a request for a package that is here:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "nothing to request") {
		t.Errorf("stderr does not say why: %q", errb.String())
	}
}

// ⛔ "THE REGISTRY WOULD NOT ANSWER" IS NOT "THE PACKAGE IS MISSING". A
// report built on a failed lookup would ask for something that may well be
// published, and the person reading it cannot tell.
func TestARegistryThatWillNotAnswerIsNotAnAbsence(t *testing.T) {
	prev := newOCI
	newOCI = func() (ociAsker, error) {
		return fakeOCI{err: errors.New("503 service unavailable")}, nil
	}
	t.Cleanup(func() { newOCI = prev })
	var out, errb bytes.Buffer
	if code := runRequest([]string{"some.org/thing"}, &out, &errb); code != 1 {
		t.Errorf("code=%d, want 1", code)
	}
	if strings.Contains(out.String(), "never published") {
		t.Errorf("an unanswered lookup was reported as an absence:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "503") {
		t.Errorf("stderr does not carry the registry's reason: %q", errb.String())
	}
}

// ⛔ AND "THE INDEX WOULD NOT SAY" IS NOT "NONE OF THEM WORKS HERE" EITHER.
// The first live run printed "2 version(s) published, none this platform can
// use" for a package whose index had simply not been written yet — a
// sentence that reads as a measured absence and was a failed lookup, in a
// report meant to be pasted into an issue.
func TestAnUnreadableIndexIsNotAnAbsenceEither(t *testing.T) {
	withOCI(t, fakeOCI{tags: []string{"1.0.0", "1.1.0"}, perr: errors.New("platform not in index")})
	var out, errb bytes.Buffer
	if code := runRequest([]string{"some.org/thing"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	text := out.String()
	if strings.Contains(text, "none this platform can use") {
		t.Errorf("a failed lookup was reported as an absence:\n%s", text)
	}
	for _, want := range []string{"would not say", "not a measured absence", "platform not in index"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report does not say %q:\n%s", want, text)
		}
	}
}

// ⛔ THE CASE THIS COMMAND EXISTS FOR, and the one it got wrong first.
//
// ghcr answers 403 at the token endpoint for a repository that does not
// exist. A user asking for a package we had never built saw that raw, read
// it as a permissions problem, and reached for this tool — which treated
// every ListTags error as "could not ask" and reprinted the 403 instead of
// the report.
func TestGhcrsRefusalForAnAbsentRepositoryIsAnAbsence(t *testing.T) {
	withOCI(t, fakeOCI{err: &errcode.ErrorResponse{StatusCode: http.StatusNotFound}})
	var out, errb bytes.Buffer
	if code := runRequest([]string{"opendev.org/openstack/python-openstackclient"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d, want 0 — an absence is a report, not a failure\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "never published in this registry") {
		t.Errorf("the absence was not reported as one:\n%s", out.String())
	}
	if strings.Contains(errb.String(), "denied") || strings.Contains(errb.String(), "403") {
		t.Errorf("the registry's raw refusal reached the user: %q", errb.String())
	}
}

func TestRequestUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runRequest(nil, &out, &errb); code != 2 {
		t.Errorf("no project = %d, want 2", code)
	}
	if code := runRequest([]string{"--nope", "a"}, &out, &errb); code != 2 {
		t.Errorf("unknown flag = %d, want 2", code)
	}
	if code := runRequest([]string{"a", "b"}, &out, &errb); code != 2 {
		t.Errorf("two projects = %d, want 2", code)
	}
}
