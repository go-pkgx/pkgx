package app

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/go-pkgx/bottle"
)

// `pkgs request` — turn "this package is missing" into a report somebody can
// act on, WITHOUT sending anything.
//
// # WHY THIS SHAPE
//
// The thing that prompted it, verbatim from a user on 2026-10-22:
//
//	pkgx openstack user list --domain stratuslab
//	pkgx: opendev.org/openstack/python-openstackclient is not published in
//	      ghcr.io/go-pkgx/packages/… : 403: denied
//
// Two problems in one line. The registry's 403 is ghcr saying "no such
// repository" and reads as a permissions fault; and the person now has to
// work out, by hand, whether the package is missing everywhere or just for
// their platform, and then where to ask.
//
// ⛔ AND LOOK AT WHAT ELSE THAT LINE CONTAINS: `--domain stratuslab`. Any
// reporter that attached "the command that failed" would publish an
// organisation's infrastructure names into a public issue tracker, from a
// machine whose owner was debugging, not publishing. So the payload is a
// FIXED LIST of fields assembled here — never argv, never the environment,
// never a path, never a hostname — and the whole report is printed for the
// person to read before anything leaves the machine.
//
// # IT SENDS NOTHING
//
// Default: print the report. `--url` prints a prefilled issue link to open
// if they choose. There is no network call that reports anything, no opt-out
// flag to forget, and nothing to configure: the only way a report reaches
// GitHub is a person clicking a link they have already read.
//
// What it DOES use the network for is the facts, because a report that
// guesses is worse than none: it asks the registry what exists, so the
// request says which platforms do have it and which does not.
func runRequest(args []string, stdout, stderr io.Writer) int {
	var project string
	wantURL := false
	for _, a := range args {
		switch {
		case a == "--url":
			wantURL = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, pfx()+"request: unknown flag %q\n", a)
			return 2
		case project == "":
			project = a
		default:
			fmt.Fprintln(stderr, pfx()+"request: one project at a time")
			return 2
		}
	}
	if project == "" {
		fmt.Fprintln(stderr, pfx()+"usage: "+tool+" request [--url] <project>")
		return 2
	}

	f, err := gatherRequest(project)
	if err != nil {
		fmt.Fprintf(stderr, pfx()+"request: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, f.report())
	if f.published {
		// ⛔ A request for something that is already here is the one answer
		// this must refuse to help with: it would file an issue whose
		// premise is false, and the next one would be trusted less.
		fmt.Fprintf(stderr, pfx()+"nothing to request: %s is published for %s\n", project, hostPlatform())
		return 1
	}
	if wantURL {
		fmt.Fprintf(stdout, "\nTo ask for it, open:\n%s\n", f.issueURL())
	} else {
		fmt.Fprintf(stdout, "\nNothing was sent. `%s request --url %s` prints a link that opens this as an issue.\n", tool, project)
	}
	return 0
}

// sayHowToAsk points at `pkgs request` when a run failed because the
// package is simply not here.
//
// ⛔ THE DEAD END IS THE DEFECT. The user who prompted this ran
//
//	pkgx openstack user list --domain stratuslab
//
// and got ghcr's 403, which names no next step and reads as a permissions
// fault. The package existed in the pantry and we had never built it — a
// five-minute fix for whoever runs the factory, and nothing in the output
// said so or where to say it.
//
// ⛔ IT MATCHES A TYPE, NOT A SENTENCE. The first version asked
// bottle.IsNotPublished, whose last resort reads the error's TEXT — and
// its first live run fired on
//
//	pkgx: command not found in the requested packages: true
//
// where the package was published and the COMMAND was missing. A hint that
// appears on unrelated failures teaches people to ignore hints.
//
// ⛔ AND IT ASKS ONE QUESTION, BECAUSE THE FIRST VERSION ANSWERED HALF OF
// IT. *bottle.NotPublishedError alone means "the registry has no such
// repository" — a brand-new package, the RARE case. The ordinary one is a
// recipe with no bottle for your platform: 567 of 1940 projects in the
// published darwin/aarch64 catalogue on 2026-10-23, against which v0.25.0
// shipped a hint that could not fire. bottle.ProjectToRequest (v0.53.0)
// answers both and names the project, so this stays one call; two
// predicates here is exactly how the second one came to be forgotten.
//
// Nothing else earns a hint: a build that failed, a signature that did not
// verify and a registry having a bad day are not things to ask somebody to
// publish.
func sayHowToAsk(err error, stderr io.Writer) {
	project := bottle.ProjectToRequest(err)
	if project == "" {
		return
	}
	fmt.Fprintf(stderr, "%sif it should be here: `%s request %s` prints a report you can open as an issue (it sends nothing)\n",
		pfx(), pkgsTool, project)
}

// pkgsTool is the sibling binary's name. `tool` is whichever of the two is
// running, and the verb lives in pkgs — so a pkgx user must be sent there
// by name rather than to a verb pkgx does not have.
const pkgsTool = "pkgs"

// requestFacts is the whole payload. Every field is here on purpose and
// there is no map, no "extra" and no free text from the environment: a
// reader can see the entire set in one screen, which is the only way a
// promise about what is sent can be checked.
type requestFacts struct {
	project   string
	platform  string // the host, in pkgx's spelling
	version   string // this tool's version
	published bool   // already here for this platform
	elsewhere []string
	versions  int
	indexErr  string // the index would not say which platforms: NOT an absence
}

func gatherRequest(project string) (requestFacts, error) {
	f := requestFacts{project: project, platform: hostPlatform(), version: version}
	c, err := newOCI()
	if err != nil {
		return f, err
	}
	tags, err := c.ListTags(project)
	switch {
	// ⛔ THE CASE THIS COMMAND EXISTS FOR. ghcr answers 403 at the token
	// endpoint for a repository that does not exist, which reads as a
	// credentials problem and is not one — it is exactly the error the
	// person was staring at when they reached for this. Treating it as a
	// failed lookup made the tool reprint the 403 it was written to
	// explain.
	case bottle.IsNotPublished(err):
		return f, nil // never published here, for anyone
	case err != nil:
		// ⛔ And not knowing is still not an absence: a registry that would
		// not answer must not become a report claiming the package is
		// missing.
		return f, fmt.Errorf("asking the registry about %s: %w", project, err)
	}
	// ⛔ bottle.IsVersionTag, NOT "anything that is not a platform tag".
	// ghcr implements no referrers API, so ORAS keeps each bottle's
	// attestations under a fallback tag named `sha256-<digest>`, and those
	// sit in the same listing. The first live run of this command filtered
	// with IsPlatformTag alone and reported a project with no releases as
	// "2 version(s) tagged" — the two being referrers. bottle owns that
	// rule and had tested it; it was unexported until go-pkgx/bottle#155,
	// and a second copy here is how two answers to one question begin.
	var vers []string
	for _, t := range tags {
		if bottle.IsVersionTag(t) {
			vers = append(vers, t)
		}
	}
	f.versions = len(vers)
	if len(vers) == 0 {
		return f, nil // never published here, for anyone
	}
	sort.Strings(vers)
	newest := vers[len(vers)-1]
	// ⛔ "THE INDEX WOULD NOT SAY" IS ITS OWN STATE. The first live run of
	// this command printed "2 version(s) published, none this platform can
	// use" for a package whose index simply had not been written yet — a
	// sentence that reads as a measured absence and was a failed lookup.
	plats, err := c.Platforms(project, newest)
	if err != nil {
		f.indexErr = err.Error()
		return f, nil
	}
	for _, p := range plats {
		if p == f.platform {
			f.published = true
		} else {
			f.elsewhere = append(f.elsewhere, p)
		}
	}
	return f, nil
}

func (f requestFacts) report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "project   %s\n", f.project)
	fmt.Fprintf(&b, "platform  %s\n", f.platform)
	fmt.Fprintf(&b, "%s      %s\n", tool, f.version)
	switch {
	case f.versions == 0:
		fmt.Fprintf(&b, "state     never published in this registry, for any platform\n")
	case f.published:
		fmt.Fprintf(&b, "state     published for this platform\n")
	case f.indexErr != "":
		fmt.Fprintf(&b, "state     %d version(s) tagged, and the index would not say which\n", f.versions)
		fmt.Fprintf(&b, "          platforms they are for: %s\n", f.indexErr)
		fmt.Fprintf(&b, "          (that is a failed lookup, not a measured absence)\n")
	case len(f.elsewhere) > 0:
		fmt.Fprintf(&b, "state     published for %s, NOT for %s\n", strings.Join(f.elsewhere, " "), f.platform)
	default:
		fmt.Fprintf(&b, "state     %d version(s) published, none this platform can use\n", f.versions)
	}
	return b.String()
}

// issueURL prefills GitHub's new-issue form. A link, not a call: the person
// sees the title and body in their browser and decides.
func (f requestFacts) issueURL() string {
	title := "please publish " + f.project
	if len(f.elsewhere) > 0 {
		title += " for " + f.platform
	}
	q := url.Values{}
	q.Set("title", title)
	q.Set("body", f.report()+
		"\nAsked for with `"+tool+" request`, which sends nothing by itself:\n"+
		"this text is the whole of what it collected.\n")
	return "https://github.com/go-pkgx/packages/issues/new?" + q.Encode()
}

func hostPlatform() string {
	osn, arch := bottle.HostSlug()
	return osn + "/" + arch
}

// newOCI is a seam: the tests need neither a registry nor a network.
var newOCI = func() (ociAsker, error) { return bottle.NewOCIClient(bottle.DistBase) }

type ociAsker interface {
	ListTags(project string) ([]string, error)
	Platforms(project, ver string) ([]string, error)
}
