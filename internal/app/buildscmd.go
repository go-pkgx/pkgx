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
		// ⛔ THE FLAGS COME FIRST, AND THE USAGE HAS TO SAY SO. Go's flag
		// package stops parsing at the first non-flag argument, so
		// `builds fftw.org@3.3.11 --platform linux/x86-64` treats the flag
		// as a second operand and exits 2 — with a usage line that, spelt
		// the other way round, showed exactly what had just failed.
		fmt.Fprintln(stderr, pfx()+"usage: "+tool+" builds [--platform os/arch] [--sort key] <project>@<version>")
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
	// ⛔⛔ A TRUST SET THAT CANNOT BE LOADED IS A CONFIGURATION ERROR, AND IT
	// MUST NOT ARRIVE AS A COLUMN OF "?". Found by running the real binary
	// against the real registry with a deliberately unmeetable threshold:
	//
	//   ZPKG_TRUST_THRESHOLD=2 with one trusted key
	//   →  HASH  INSTALLED  PUBLISHED  SIGNED BY
	//      (none)  -        yes        ?
	//
	// "?" is this table's word for "the registry was never asked", so the
	// reader is told their network failed when in fact their configuration
	// can never be satisfied. The error was perfectly good — LoadTrustSet
	// says "cannot be met: the trust set has 1 key(s)" — and it was thrown
	// away by describeBuilds, which drops every error so a listing survives
	// an unreachable registry.
	//
	// So ask HERE, before drawing anything. A command validates its own
	// configuration before it draws a table whose columns depend on it.
	if _, err := bottle.LoadTrustSet(); err != nil {
		fmt.Fprintln(stderr, pfx()+"builds: "+err.Error())
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
	fmt.Fprintf(tw, "HASH\tINSTALLED\t%s\tSIGNED BY\tBUILT BY\tWHAT\n", published)
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", hash, installed, pub, signedBy(b), builtBy(b), what[i])
	}
	tw.Flush()
	// ⛔ A ROW THAT WOULD BE REFUSED AT INSTALL TIME SAYS SO UNDER THE TABLE.
	// The cell alone reads as a description; the reader's actual question is
	// "can I install this?", and the answer is yes or no, not a list of
	// names. Said once, and only when there is such a row.
	//
	// ⛔ NO NUMBER IN IT. The first draft printed the threshold of the first
	// failing row under a sentence about EVERY marked row — true today only
	// because one trust set describes them all, and silently wrong the moment
	// that stops holding. The variable is named; its value is the reader's
	// own and they can print it.
	//
	// ⛔ AND IT DOES NOT CLAIM ZPKG_VERIFY=0 WARNS. It does not: the check
	// returns early and says nothing (bottle's verifyPulledDigest). Writing
	// "installs anyway, and says so" would have been a promise this stack
	// does not keep.
	for _, b := range builds {
		if b.Signatures != nil && !b.Signatures.OK() {
			fmt.Fprintf(w, "\n⚠ a row marked ⚠ does not meet your %s and will be REFUSED at install time\n  (%s=0 skips the check entirely, silently).\n",
				bottle.TrustThresholdEnv, "ZPKG_VERIFY")
			break
		}
	}
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

// signedBy is the SIGNED BY cell: who vouches for this build, as THIS
// machine's trust set names them.
//
// ⛔⛔ "NOT ASKED" IS NOT "NOBODY", AND THE TWO MUST NOT RENDER ALIKE. A nil
// report means the registry was never reached for this row — printing
// "unsigned" there would turn a network failure into a claim about the
// artefact, which is exactly how someone ends up trusting a bottle nobody
// signed, or rebuilding one that was fine. "?" is the same convention the
// PUBLISHED column already uses, for the same reason.
//
// ⛔ AND THE NAMES ARE OURS, NOT THE REGISTRY'S. A name on the artefact is
// written by whoever pushed it; what appears here comes from the local trust
// set, which is the only place a name means anything.
func signedBy(b bottle.Build) string {
	if b.Signatures == nil {
		return "?"
	}
	if b.Signatures.OK() {
		return b.Signatures.String()
	}
	return "⚠ " + b.Signatures.String()
}

// builtBy is the BUILT BY cell: which factory made this build, read from its
// in-toto provenance.
//
// ⛔⛔ IT IS A COLUMN AND NOT PART OF WHAT, BECAUSE WHAT SAYS WHAT DIFFERS.
// The builder is usually the same on every row, and a constant in a column
// whose documented contract is "the axes on which these builds differ FROM
// EACH OTHER" would break that contract for every reader who relies on it.
//
// ⛔ AND IT IS NOT IN THE HASH, WHICH IS WHY THE COLUMN EXISTS AT ALL. A zbk
// release must not re-address every bottle in the registry — Nix and Spack
// take the same position — so two factories building identical inputs produce
// the SAME identity, and this is the only thing that tells them apart.
//
// Three states, kept apart on purpose:
//
//	?               the registry was never asked (the PUBLISHED column's own
//	                convention, same reason)
//	not recorded    it was asked, and the bottle carries no provenance —
//	                which is most of the registry today
//	bk (no version) a builder id that names a tool and not a release
//	zbk@v0.64.0     what built it
//
// ⛔⛔ "bk" ALONE WOULD READ AS A COMPLETE ANSWER, AND IT IS NOT ONE. Asked of
// the live registry on 2026-11-05, lz4.org 1.10.0 answers
// `https://github.com/go-pkgx/bk@v0.26.0` on linux/x86-64 and
// `https://github.com/go-pkgx/bk` — no version at all — on darwin/aarch64.
// That is the defect zbk's own builderWithVersion comment records ("A BUILDER
// THAT NEVER CHANGES CANNOT TELL TWO BUILDS APART"), still sitting in the
// registry on every bottle published before it was fixed. Printing a bare
// `bk` would hand the reader a tool name shaped exactly like an answer, so
// the cell says which kind of answer it is.
func builtBy(b bottle.Build) string {
	if b.Signatures == nil {
		return "?"
	}
	if b.Builder == "" {
		return "not recorded"
	}
	s := bottle.ShortBuilder(b.Builder)
	if !strings.Contains(s, "@") {
		return s + " (no version)"
	}
	return s
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
