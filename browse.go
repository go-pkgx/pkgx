package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-pkgx/bottle"
)

// Browsing the tree, and completing into it.
//
// # WHY THE BINARY DOES ITS OWN COMPLETION
//
// Spack generates `spack-completion.bash` from its command tree and commits
// it; Guix hand-writes `etc/completion/bash/guix`. Both then need a check
// that the generated file still matches the program. Nix took the other
// road: the `nix` binary answers completion queries itself when
// NIX_GET_COMPLETIONS names the argument being completed, and the shell
// function is a few lines that call it.
//
// Nix's is the one copied here, for a reason specific to this project: the
// thing being completed is not a fixed command tree, it is the REGISTRY —
// which changes without the binary changing, and which a generated file
// could never be level with. A snippet that asks the binary is also the only
// shape that works in a scratch image, where there is no completion
// framework, no python and no generator to run.
//
// The protocol:
//
//	PKGX_GET_COMPLETIONS=<n> pkgx <arg0> <arg1> … <argN>
//
// prints one `value\tdescription` per line and exits 0. The description is
// what zsh and fish show beside the candidate; bash ignores it.
//
// n indexes the arguments AS PKGX RECEIVES THEM — `pkgx` itself is not one
// of them. Every shell counts its own words WITH the command, so each
// snippet below subtracts one. That off-by-one is not a detail: with it
// wrong, `pkgx +gnu.org/ba<TAB>` reads past the end of the arguments, finds
// the empty string, and offers the subcommand list — which is what this
// did when first run against a real store.

// catalogFor is a seam: a test must not need a registry, and the fallback
// below must be reachable.
var catalogFor = func() (bottle.Catalog, error) {
	// A LOCAL catalogue wins, because the case it serves is the one the
	// registry cannot: an air-gapped or offline image, where the file is
	// shipped beside the store rather than fetched. It is also how a
	// person inspects a catalogue before publishing it.
	//
	// A path that is set and unreadable is an ERROR, not a quiet fallback
	// to the registry: somebody who sets PKGX_CATALOG means that file.
	if p := os.Getenv("PKGX_CATALOG"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return bottle.Catalog{}, err
		}
		return bottle.UnmarshalCatalog(b)
	}
	c, err := bottle.NewOCIClient(bottle.DistBase)
	if err != nil {
		return bottle.Catalog{}, err
	}
	osn, arch := bottle.HostSlug()
	return bottle.FetchCatalog(c, osn, arch)
}

// installedCatalog is what this machine HAS, read from the store.
//
// The fallback, and not a lesser one: in a scratch image before the first
// fetch, and on any machine that is offline, it is the only true answer
// available — and it is the one a user most often wants, because completing
// onto something already installed costs nothing to run.
//
// A project directory is one that holds `v<something>` children; everything
// above it is a namespace. That rule is what makes `gnu.org` an interior
// node and `gnu.org/bash` a leaf without a list of either.
func installedCatalog(dir string) bottle.Catalog {
	cat := bottle.Catalog{Generated: time.Unix(0, 0).UTC().Format(time.RFC3339)}
	seen := map[string][]string{}
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		if depth > 6 { // the deepest real project is three segments
			return
		}
		ents, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			return
		}
		var vers []string
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			if strings.HasPrefix(e.Name(), "v") && len(e.Name()) > 1 && e.Name()[1] >= '0' && e.Name()[1] <= '9' {
				vers = append(vers, strings.TrimPrefix(e.Name(), "v"))
				continue
			}
			// A dot-directory is not a namespace. $PKGX_DIR holds at least
			// `.local/tmp`, and listing it as a root told the user there is
			// a package family called `.local`.
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			child := e.Name()
			if rel != "" {
				child = rel + "/" + e.Name()
			}
			walk(child, depth+1)
		}
		if len(vers) > 0 && rel != "" {
			seen[rel] = vers
		}
	}
	walk("", 0)
	osn, arch := bottle.HostSlug()
	for proj, vers := range seen {
		sort.Sort(sort.Reverse(sort.StringSlice(vers)))
		cat.Projects = append(cat.Projects, bottle.CatalogProject{
			Project: proj, Versions: vers, Platforms: []string{osn + "/" + arch},
		})
	}
	sort.Slice(cat.Projects, func(i, j int) bool { return cat.Projects[i].Project < cat.Projects[j].Project })
	return cat
}

// browseCatalog is the catalogue a browse or a completion reads: the
// registry's if it can be had, otherwise the store's.
//
// The two are NOT merged. A merged list would say "available" about things
// from two sources with different truth — one of them a cache of unknown age
// — and `pkgx ls` would have no honest header to print. Which one answered
// is returned, so the caller can say.
func browseCatalog(dir string) (bottle.Catalog, string, error) {
	named := os.Getenv("PKGX_CATALOG")
	c, err := catalogFor()
	if err != nil && named != "" {
		// NAMED and unreadable is a user's mistake, not a reason to show
		// something else. Falling back here would answer a question about
		// the file they pointed at with facts from the local store, under
		// a header blaming "the registry" — three wrongs in one line, and
		// the comment above catalogFor promised otherwise.
		return bottle.Catalog{}, named, err
	}
	if err == nil && len(c.Projects) > 0 {
		// Which one, exactly. A header that said "registry" over a
		// catalogue read from PKGX_CATALOG would be the kind of small lie
		// that makes a person doubt the rest of the output.
		if named != "" {
			return c, named, nil
		}
		return c, "registry", nil
	}
	return installedCatalog(dir), "installed", nil
}

// runLs prints what is available under a node.
// runLs shows what is under a node — and a node has TWO kinds of thing
// under it.
//
//	pkgx ls gnu.org        what that namespace CONTAINS
//	pkgx ls curl.se        what that package NEEDS
//
// The same words, "what is available under this node", mean containment at
// a namespace and dependency at a package, and a browser has to answer
// whichever the node is. Nix, Guix and Spack keep the two apart in separate
// commands; here the node decides, because the person typing already knows
// which kind of thing they named.
//
// `--tree` descends, with `--depth` to bound it. `guix graph --max-depth`
// exists because a full transitive graph of anything interesting is pages
// long, and the first level is what a person reads.
func runLs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tree := fs.Bool("tree", false, "descend through the dependencies, not just the first level")
	depth := fs.Int("depth", 0, "how far --tree descends; 0 is no limit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prefix := ""
	if fs.NArg() > 0 {
		prefix = fs.Arg(0)
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "pkgx: usage: pkgx ls [--tree] [--depth N] [node]")
		return 2
	}
	cat, src, err := browseCatalog(bottle.Dir())
	if err != nil {
		fmt.Fprintf(stderr, "pkgx: PKGX_CATALOG names %s and it cannot be read: %v\n", src, err)
		return 2
	}
	say := func() {
		if src == "installed" {
			fmt.Fprintln(stderr, "pkgx: showing what is INSTALLED — the registry catalogue could not be read")
		} else {
			fmt.Fprintf(stderr, "pkgx: %s catalogue, %s\n", src, cat.Age(time.Now()))
		}
	}

	// A PACKAGE node: what it needs. Checked before the namespace children,
	// because `curl.se` is both a package and a namespace and the package
	// is what somebody typing its full name meant.
	node := strings.TrimSuffix(prefix, "/")
	if p, ok := cat.Lookup(node); ok && !strings.HasSuffix(prefix, "/") {
		say()
		fmt.Fprintf(stdout, "%s%s\n", p.Project, versionNote(p))
		d := *depth
		if !*tree {
			d = 1
		}
		sub := cat.DepTree(node, d)
		if len(sub) == 0 {
			fmt.Fprintln(stdout, "  (declares no runtime dependencies)")
		}
		printDepTree(stdout, sub, 1)
		// A namespace of the same name is still worth naming, or
		// `curl.se/ca-certs` becomes unreachable from `curl.se`.
		if kids := cat.Children(node); len(kids) > 0 {
			fmt.Fprintf(stdout, "\nalso a namespace, %d under it: pkgx ls %s/\n", len(kids), node)
		}
		return 0
	}

	kids := cat.Children(prefix)
	if len(kids) == 0 {
		fmt.Fprintf(stderr, "pkgx: nothing under %q in the %s catalogue\n", prefix, src)
		return 1
	}
	say()
	for _, n := range kids {
		fmt.Fprintf(stdout, "%-40s %s\n", n.Name, lsNote(cat, n))
	}
	return 0
}

// printDepTree draws the subtree the way `pkgx --graph` already draws one.
func printDepTree(w io.Writer, ns []bottle.DepNode, level int) {
	for _, n := range ns {
		note := ""
		switch {
		case !n.Known:
			// Said out loud: a name with nothing behind it is either a hole
			// in the catalogue or a project resolved from elsewhere, and a
			// browser that printed it like any other node would hide both.
			note = "  (not in this catalogue)"
		case n.Repeat:
			note = "  (shown above)"
		case n.Version != "":
			note = "  " + n.Version
		}
		fmt.Fprintf(w, "%s%s%s\n", strings.Repeat("  ", level), n.Project, note)
		printDepTree(w, n.Under, level+1)
	}
}

func versionNote(p bottle.CatalogProject) string {
	if len(p.Versions) == 0 {
		return ""
	}
	return " — " + strings.Join(p.Versions, " ")
}

// lsNote says what a node is in the fewest words that distinguish the three
// cases — a package, a namespace, or both.
func lsNote(cat bottle.Catalog, n bottle.Node) string {
	var parts []string
	if n.Leaf {
		if p, ok := cat.Lookup(n.Name); ok && len(p.Versions) > 0 {
			parts = append(parts, p.Versions[0])
		} else {
			parts = append(parts, "a package")
		}
	}
	if n.Under > 0 {
		parts = append(parts, fmt.Sprintf("%d under", n.Under))
	}
	return strings.Join(parts, ", ")
}

// completionsEnv is the environment variable a shell sets to ask for
// completions. Named after Nix's, because it is Nix's protocol.
const completionsEnv = "PKGX_GET_COMPLETIONS"

// maybeComplete answers a completion request and reports whether it did.
//
// Called before anything else in main: a completion must not install, fetch
// or run anything, and the surest way to guarantee that is to answer and
// leave before the rest of the program exists.
func maybeComplete(argv []string, getenv func(string) string, stdout io.Writer) bool {
	raw := getenv(completionsEnv)
	if raw == "" {
		return false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		// A malformed request is answered with nothing rather than with a
		// diagnostic: whatever is on the other end is a shell's completion
		// buffer, and text it did not ask for lands in the user's prompt.
		return true
	}
	word := ""
	if n < len(argv) {
		word = argv[n]
	}
	for _, c := range completionsFor(word) {
		fmt.Fprintf(stdout, "%s\t%s\n", c.value, c.note)
	}
	return true
}

type completion struct{ value, note string }

// completionsFor is the completion of ONE word.
//
// A `+pkg` word keeps its plus: the shell replaces the whole word, so a
// candidate that dropped it would turn `pkgx +gnu.org/ba<TAB>` into
// `pkgx gnu.org/bash` — a different command.
func completionsFor(word string) []completion {
	plus := strings.HasPrefix(word, "+")
	bare := strings.TrimPrefix(word, "+")

	// A word that is not a package at all: offer the subcommands, once,
	// when nothing has been typed that looks like a path.
	var out []completion
	if !plus && !strings.Contains(bare, "/") && !strings.Contains(bare, ".") {
		for _, s := range subcommandCompletions {
			if strings.HasPrefix(s.value, bare) {
				out = append(out, s)
			}
		}
	}
	// A completion says NOTHING about a failure. The other end is a shell's
	// completion buffer and a diagnostic there lands in the user's prompt,
	// so an unreadable catalogue simply offers no package names.
	cat, _, _ := browseCatalog(bottle.Dir())
	for _, n := range cat.Complete(bare) {
		v := n.Name
		if plus {
			v = "+" + v
		}
		// A namespace completes WITH its slash, so the next TAB descends
		// instead of re-offering the same node.
		if !n.Leaf && n.Under > 0 {
			v += "/"
		}
		out = append(out, completion{v, lsNote(cat, n)})
	}
	return out
}

var subcommandCompletions = []completion{
	{"ls", "what is available under a node"},
	{"env", "environments: init, load, unload, purge, avail, show, import"},
	{"compat", "how much of a package set resolves under a base"},
	{"completion", "print the shell completion snippet"},
}

// runCompletionSnippet prints the few lines a shell needs.
//
// Few, because they do nothing but call the binary: the registry changes
// without pkgx changing, so anything cleverer here would be a second place
// to keep level with the first. There is no completion framework to install
// and nothing to generate, which is what makes this work in a scratch image.
func runCompletionSnippet(shell string, stdout, stderr io.Writer) int {
	s, ok := completionSnippets[shell]
	if !ok {
		fmt.Fprintf(stderr, "pkgx: usage: pkgx completion bash|zsh|fish\n")
		return 2
	}
	fmt.Fprint(stdout, s)
	return 0
}

var completionSnippets = map[string]string{
	"bash": `# pkgx completion — eval "$(pkgx completion bash)"
_pkgx_complete() {
  local i n=0 words=()
  for ((i = 0; i < ${#COMP_WORDS[@]}; i++)); do
    words+=("${COMP_WORDS[i]}")
    [ "$i" -lt "$COMP_CWORD" ] && n=$((n + 1))
  done
  local IFS=$'\n'
  COMPREPLY=($(PKGX_GET_COMPLETIONS=$((COMP_CWORD - 1)) pkgx "${words[@]:1}" 2>/dev/null | cut -f1))
}
complete -o nospace -F _pkgx_complete pkgx
`,
	"zsh": `# pkgx completion — eval "$(pkgx completion zsh)"
_pkgx_complete() {
  local -a lines
  lines=(${(f)"$(PKGX_GET_COMPLETIONS=$((CURRENT - 1)) pkgx ${words[2,-1]} 2>/dev/null)"})
  local -a vals descs
  local l
  for l in $lines; do
    vals+=("${l%%	*}")
    descs+=("${l%%	*}:${l#*	}")
  done
  _describe -t pkgx 'pkgx' descs vals -S ''
}
compdef _pkgx_complete pkgx
`,
	"fish": `# pkgx completion — pkgx completion fish | source
function __pkgx_complete
  set -l words (commandline -opc) (commandline -ct)
  PKGX_GET_COMPLETIONS=(math (count $words) - 2) pkgx $words[2..-1] 2>/dev/null
end
complete -c pkgx -f -a '(__pkgx_complete)'
`,
}
