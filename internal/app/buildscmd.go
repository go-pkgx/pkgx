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
	if err := fs.Parse(args); err != nil {
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
	printBuilds(stdout, builds, err != nil)
	fmt.Fprintf(stdout, "\ninstalled under %s; name one with: %s@%s/<hash>\n", bottle.Dir(), project, version)
	return boolExit(err != nil)
}

// printBuilds renders the table. The hash is first because it is the thing a
// caller copies into `@version/<hash>`.
func printBuilds(w io.Writer, builds []bottle.Build, registryUnknown bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	published := "PUBLISHED"
	if registryUnknown {
		published = "PUBLISHED?"
	}
	fmt.Fprintf(tw, "HASH\tINSTALLED\t%s\n", published)
	for _, b := range builds {
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
		fmt.Fprintf(tw, "%s\t%s\t%s\n", hash, installed, pub)
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

// boolExit is 1 for "something went wrong", 0 otherwise.
func boolExit(bad bool) int {
	if bad {
		return 1
	}
	return 0
}
