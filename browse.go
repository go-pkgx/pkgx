package main

import (
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
func browseCatalog(dir string) (bottle.Catalog, string) {
	if c, err := catalogFor(); err == nil && len(c.Projects) > 0 {
		return c, "registry"
	}
	return installedCatalog(dir), "installed"
}

// runLs prints what is available under a node.
func runLs(args []string, stdout, stderr io.Writer) int {
	prefix := ""
	if len(args) > 0 {
		prefix = args[0]
	}
	if len(args) > 1 {
		fmt.Fprintln(stderr, "pkgx: usage: pkgx ls [node]")
		return 2
	}
	cat, src := browseCatalog(bottle.Dir())
	kids := cat.Children(prefix)
	if len(kids) == 0 {
		// A node with nothing under it is either a leaf or absent, and the
		// two must not print alike: one is an answer, the other is a
		// mistake the user can fix.
		if p, ok := cat.Lookup(strings.TrimSuffix(prefix, "/")); ok {
			fmt.Fprintf(stdout, "%s is a package, not a namespace", p.Project)
			if len(p.Versions) > 0 {
				fmt.Fprintf(stdout, " — %s", strings.Join(p.Versions, " "))
			}
			fmt.Fprintln(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "pkgx: nothing under %q in the %s catalogue\n", prefix, src)
		return 1
	}
	if src == "installed" {
		fmt.Fprintln(stderr, "pkgx: showing what is INSTALLED — the registry catalogue could not be read")
	} else {
		fmt.Fprintf(stderr, "pkgx: %s catalogue, %s\n", src, cat.Age(time.Now()))
	}
	for _, n := range kids {
		fmt.Fprintf(stdout, "%-40s %s\n", n.Name, lsNote(cat, n))
	}
	return 0
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
	cat, _ := browseCatalog(bottle.Dir())
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
