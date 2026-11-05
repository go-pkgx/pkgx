package app

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/go-zpkg/bottle"
)

// `zpkgs builds <project>@<version>` — which builds of one version exist.
//
// # WHY IT HAD TO EXIST
//
// The resolver refuses an ambiguous version, correctly:
//
//	fftw.org 3.3.11 was published as 2 different builds and nothing here
//	says which: a variant hash is not a version, so there is no newest one
//	to pick
//
// A refusal that cannot be answered is half a design. The user is told that
// two builds exist and given no way to see them or to choose one. This is the
// other half, with `@version/<hash>` on the resolver side.
//
// # ONE LIST, TWO SOURCES
//
// The store and the registry are joined by hash, because a user asking "which
// builds are there" does not mean one of them: a list of only what is
// installed cannot answer *what else could I have*, and a list of only what is
// published cannot answer *what do I already have*.
//
// # IT NEEDS NO NETWORK TO BE USEFUL
//
// The installed half is a directory listing. When the registry cannot be
// reached the installed half is still printed and the failure is REPORTED —
// an unobtained answer must not read as "nothing is published", because that
// is how a user concludes a build does not exist and rebuilds it.
func runBuilds(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("builds", flag.ContinueOnError)
	fs.SetOutput(stderr)
	platform := fs.String("platform", "", "ask about `os/arch` instead of this machine")
	// ⛔ THE KEYS ARE bottle's, NOT A SECOND LIST. A flag that enumerated
	// them here would disagree with the library the first time one was
	// added, and the disagreement would be a usage string that lies.
	sortBy := fs.String("sort", string(bottle.ByStatus), sortFlagHelp())
	if err := fs.Parse(args); err != nil {
		return 2
	}
	order, err := bottle.ParseBuildOrder(*sortBy)
	if err != nil {
		fmt.Fprintln(stderr, pfx()+"builds --sort: "+err.Error())
		// ⛔ AND "date" IS ANSWERED, NOT JUST REFUSED. It is the key a reader
		// reaches for first, and its absence is a property of the design
		// rather than an oversight — so the refusal explains it instead of
		// leaving them to assume the feature is missing.
		if *sortBy == "date" {
			fmt.Fprintln(stderr, pfx()+bottle.WhyNoDate)
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, pfx()+"usage: "+tool+" builds <project>@<version> [--platform os/arch]")
		return 2
	}
	project, version, ok := strings.Cut(fs.Arg(0), "@")
	if !ok || project == "" || version == "" {
		// ⛔ THE VERSION IS REQUIRED, AND THE MESSAGE SAYS WHY. "builds of
		// fftw.org" is not a question this can answer: builds are siblings
		// WITHIN a version, and listing every build of every version would
		// answer a question nobody asked with a page nobody can read.
		fmt.Fprintf(stderr, pfx()+"builds: %q names no version — builds are the siblings of ONE version, so say which (e.g. %s@3.3.11)\n",
			fs.Arg(0), project)
		return 2
	}
	osn, arch := bottle.HostSlug()
	if *platform != "" {
		o, a, cut := strings.Cut(*platform, "/")
		if !cut || o == "" || a == "" {
			fmt.Fprintf(stderr, pfx()+"builds --platform: %q is not os/arch\n", *platform)
			return 2
		}
		osn, arch = o, a
	}

	builds, err := bottle.BuildsOf(project, version, bottle.Dir(), osn, arch)
	bottle.SortBuilds(project, builds, order)
	// ⛔ THE ERROR DOES NOT SUPPRESS THE ANSWER. BuildsOf returns the
	// installed half alongside a registry failure precisely so this can print
	// what it knows and say what it could not learn.
	if err != nil {
		fmt.Fprintf(stderr, pfx()+"builds: the registry could not be asked, so PUBLISHED is unknown below: %v\n", err)
	}
	if len(builds) == 0 {
		if err == nil {
			fmt.Fprintf(stdout, "no build of %s %s is installed here or published for %s/%s\n", project, version, osn, arch)
		}
		// A registry failure already printed its reason; adding "none" here
		// would state as a fact the thing we just said we do not know.
		return boolExit(err != nil)
	}
	printBuilds(stdout, project, builds, err != nil)
	// ⛔ THE WITHDRAWALS ARE RESTATED UNDER THE TABLE, saying that they still
	// RESOLVE. A reader who sees "withdrawn" in a cell and nothing else will
	// assume the build is gone; it is not, it is ranked last — and a lock
	// that pins it still works, which is the whole reason the mechanism
	// ranks rather than filters.
	if note := bottle.WithdrawnNote(project, builds); note != "" {
		fmt.Fprintf(stdout, "\n%s\n", note)
	}
	fmt.Fprintf(stdout, "\ninstalled under %s; name one with: %s@%s/<hash>\n", bottle.Dir(), project, version)
	return boolExit(err != nil)
}

// printBuilds renders the table. The hash is first because it is the thing a
// caller copies into `@version/<hash>`.
//
// ⛔⛔ EVERY ROW CARRIES A DESCRIPTION, OR THE TABLE IS A LIST OF HASHES AND
// THE READER IS NO BETTER OFF. A hash is opaque by construction; shown two of
// them with nothing beside them, a caller has no way to choose and this
// command has handed back the problem it was asked to solve. The WHAT column
// is bottle.Distinguish: the axes on which these builds differ FROM EACH
// OTHER, which is the only short answer that is also a useful one.
func printBuilds(w io.Writer, project string, builds []bottle.Build, registryUnknown bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	published := "PUBLISHED"
	if registryUnknown {
		published = "PUBLISHED?"
	}
	what := describeEach(project, builds)
	fmt.Fprintf(tw, "HASH\tINSTALLED\t%s\tWHAT\n", published)
	for i, b := range builds {
		// ⛔⛔ A BOTTLE WITH NO HASH IS NOT A BLANK CELL. Today's registry is
		// full of them: every tag published before builds had identities is
		// a plain `3.3.11`, and so is every bottle we only mirrored. Printing
		// "" there produces a row the reader cannot name, cannot pass to
		// `@version/<hash>`, and cannot tell from a rendering fault. It is
		// also the one row that is genuinely ambiguous, so it says so.
		hash := b.Hash()
		if hash == "" {
			hash = "(none)"
		}
		// ⛔ "yes", NOT THE PATH. The prefix is
		// <store>/<project>/v<version>-h<hash> — derivable from the row and
		// the footer — and printing it here made every line ~150 characters,
		// so the table wrapped and the hash, which is the one thing a reader
		// copies, was no longer in a column.
		installed := "-"
		if b.Installed != "" {
			installed = "yes"
		}
		// ⛔ "?" AND NOT "no" WHEN THE REGISTRY WAS NOT REACHED. Printing
		// "no" would turn a question we failed to ask into an answer, which
		// is the whole failure mode this column exists to avoid.
		pub := "no"
		switch {
		case registryUnknown:
			pub = "?"
		case b.Published:
			pub = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", hash, installed, pub, what[i])
	}
	tw.Flush()
	// Said once, under the table, and only when there is such a row: a
	// reader meeting "(none)" needs to know it is a fact about the bottle
	// and not about this command.
	for _, b := range builds {
		if b.Hash() == "" {
			fmt.Fprintln(w, "\n(none) is a bottle published before builds carried an identity, or one that was only mirrored:\nnothing records what it was built from, so it cannot be named by hash.")
			break
		}
	}
}

// sortFlagHelp names the keys bottle offers, so the usage cannot drift from
// the library the first time one is added.
func sortFlagHelp() string {
	names := make([]string, 0, len(bottle.BuildOrders))
	for _, o := range bottle.BuildOrders {
		names = append(names, string(o))
	}
	return "order the list by `key`: " + strings.Join(names, ", ")
}

// bottleWithdrawalOf is a seam: the withdrawal list is read once per process
// from a file, so a test that went through it would measure whichever test
// ran first.
var bottleWithdrawalOf = bottle.WithdrawalOf

// describeEach is the WHAT column: what each build is, in the terms that tell
// it apart from the others listed beside it.
//
// ⛔ THE WITHDRAWAL COMES FIRST IN THE CELL. A reader scanning the column to
// choose must not have to finish reading a dependency list before learning
// that this build is the one a CVE caught — and the replacement is in the
// same cell, because a warning that does not say where to go next leaves them
// comparing hashes, which is what the column exists to spare them.
//
// ⛔ AND A BUILD NOTHING RECORDS SAYS SO. Every bottle published before
// identities existed is one of these; an empty cell reads as a rendering
// fault rather than as the truth, which is that nothing was ever written
// down about how it was made.
func describeEach(project string, builds []bottle.Build) []string {
	specs := make([]bottle.Concrete, len(builds))
	for i, b := range builds {
		specs[i] = b.Spec
	}
	var diff []string
	if len(builds) == 1 {
		// Nothing to be told apart from, so the whole of what was recorded
		// is the useful answer — summarised.
		diff = []string{bottle.Describe(specs[0])}
	} else {
		diff = bottle.Distinguish(specs)
	}

	out := make([]string, len(builds))
	for i, b := range builds {
		var parts []string
		if w, yes := bottleWithdrawalOf(project, b.Version.Raw, b.Hash()); yes {
			parts = append(parts, "⚠ "+w.String())
		}
		switch {
		case diff[i] != "":
			parts = append(parts, diff[i])
		case len(parts) == 0:
			parts = append(parts, "nothing recorded")
		}
		out[i] = strings.Join(parts, " · ")
	}
	return out
}

// boolExit is 1 for "something went wrong", 0 otherwise.
func boolExit(bad bool) int {
	if bad {
		return 1
	}
	return 0
}
