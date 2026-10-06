package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-pkgx/bottle"
	"golang.org/x/term"
)

// `pkgx browse` — walking the tree instead of guessing at it.
//
// # WHY A SECOND WAY TO READ THE SAME THING
//
// `pkgx ls` answers one question per command. That is right for a script
// and wrong for a person who does not yet know what they are looking for:
// finding `curl.se/ca-certs` from `curl.se` costs two commands and knowing
// that the trailing slash exists. `nix-tree` is the precedent — it is a
// whole-screen browser over a store, and it exists because the same data
// read one node at a time is data nobody explores.
//
// # IT READS THE CACHE AND NOTHING ELSE
//
// No network, no resolution, no store walk per keystroke: the catalogue is
// loaded once and every key is a move within it. That is what lets this run
// in a scratch image with the network cut, which is the condition the whole
// of this work was for.
//
// # WITHOUT A TTY IT IS NOT AN ERROR
//
// `pkgx browse | less`, a CI log, a scratch image with no terminal: it
// prints the tree and exits. A command that refuses to work when its output
// is a pipe is a command people stop putting in scripts, and the data is
// the same data either way.

// browseKeys is the whole interaction, written down once so the help line
// and the key handler cannot disagree.
var browseKeys = []struct{ key, does string }{
	{"↑ ↓ / k j", "move"},
	{"→ / l / enter", "descend"},
	{"← / h", "back"},
	{"tab", "names ⇄ dependencies"},
	{"/", "search"},
	{"space", "pin for the exit line"},
	{"q", "quit, printing what is pinned"},
}

// runBrowse is the command.
func runBrowse(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "pkgx: usage: pkgx browse [node]")
		return 2
	}
	at := ""
	if len(args) == 1 {
		at = args[0]
	}
	cat, src, err := browseCatalog(bottle.Dir())
	if err != nil {
		fmt.Fprintf(stderr, "pkgx: PKGX_CATALOG names %s and it cannot be read: %v\n", src, err)
		return 2
	}
	if len(cat.Projects) == 0 {
		fmt.Fprintln(stderr, "pkgx: nothing to browse — run: pkgx catalog update")
		return 1
	}

	// No terminal: print and leave. Checked on the FILE, not on a flag,
	// because `pkgx browse | less` and a CI log are the same case and
	// neither of them passed a flag.
	f, ok := stdin.(*os.File)
	if !ok || !termIsTerminal(int(f.Fd())) {
		fmt.Fprintf(stderr, "pkgx: not a terminal — printing %s\n", nodeLabel(at))
		return runLs(argsFor(at), stdout, stderr)
	}
	return browseLoop(&browser{cat: cat, src: src, at: at}, f, stdout, stderr)
}

func argsFor(at string) []string {
	if at == "" {
		return nil
	}
	return []string{at}
}

func nodeLabel(at string) string {
	if at == "" {
		return "the roots"
	}
	return at
}

// browser is where the cursor is and what it has been told to remember.
type browser struct {
	cat     bottle.Catalog
	src     string
	at      string   // the node whose children are listed
	deps    bool     // dependencies instead of names
	sel     int      // which row
	filter  string   // the / search
	pinned  []string // what q will print
	message string
}

// rows is what the current node shows, which is one of three things and
// never a mixture.
func (b *browser) rows() []string {
	var out []string
	switch {
	case b.filter != "":
		for _, h := range b.cat.Search(b.filter) {
			out = append(out, h.Project)
		}
	case b.deps:
		for _, n := range b.cat.DepTree(strings.TrimSuffix(b.at, "/"), 1) {
			out = append(out, n.Project)
		}
	default:
		for _, n := range b.cat.Children(b.at) {
			out = append(out, n.Name)
		}
	}
	return out
}

// pin adds or removes a project from what the exit line will print.
//
// `pkgx +$(pkgx browse)` is the shape this is for, which is why q prints
// the pins to STDOUT and everything else goes to stderr: the browser's
// chrome must not end up in a shell's command line.
func (b *browser) pin(p string) {
	for i, x := range b.pinned {
		if x == p {
			b.pinned = append(b.pinned[:i], b.pinned[i+1:]...)
			b.message = "unpinned " + p
			return
		}
	}
	b.pinned = append(b.pinned, p)
	sort.Strings(b.pinned)
	b.message = "pinned " + p
}

func (b *browser) pinnedSet() map[string]bool {
	m := make(map[string]bool, len(b.pinned))
	for _, p := range b.pinned {
		m[p] = true
	}
	return m
}

// up goes to the parent node, or clears a search, whichever the reader
// most likely meant by "back".
func (b *browser) up() {
	if b.filter != "" {
		b.filter, b.sel = "", 0
		return
	}
	at := strings.TrimSuffix(b.at, "/")
	if i := strings.LastIndex(at, "/"); i >= 0 {
		b.at = at[:i]
	} else {
		b.at = ""
	}
	b.sel = 0
}

// enter descends into the selected row.
func (b *browser) enter() {
	rows := b.rows()
	if len(rows) == 0 {
		return
	}
	b.at, b.filter, b.sel = rows[b.sel], "", 0
	// A node with children lists them; a leaf has none, and switching to
	// its dependencies is what "descend" means there. Deciding here rather
	// than making the reader press tab is the whole difference between a
	// browser and a pair of commands.
	if len(b.cat.Children(b.at)) == 0 {
		b.deps = true
	}
}

// render draws one screen.
func (b *browser) render(w io.Writer, height int) {
	rows := b.rows()
	pinned := b.pinnedSet()
	swept := knowsVersions(b.cat)

	head := nodeLabel(b.at)
	if b.deps && b.filter == "" {
		head += "  (dependencies)"
	}
	if b.filter != "" {
		head = fmt.Sprintf("search %q", b.filter)
	}
	fmt.Fprintf(w, "\x1b[H\x1b[2J%s — %d\r\n", head, len(rows))

	// A window around the cursor, so a node with 452 children is navigable
	// on a 24-line terminal instead of printing 452 lines every keystroke.
	body := height - 4
	if body < 3 {
		body = 3
	}
	start := b.sel - body/2
	if start < 0 {
		start = 0
	}
	if start+body > len(rows) {
		start = len(rows) - body
		if start < 0 {
			start = 0
		}
	}
	for i := start; i < len(rows) && i < start+body; i++ {
		cursor := "  "
		if i == b.sel {
			cursor = "> "
		}
		mark := " "
		if pinned[rows[i]] {
			mark = "+"
		}
		note := ""
		if p, ok := b.cat.Lookup(rows[i]); ok {
			switch {
			case len(p.Versions) > 0:
				note = p.Versions[0]
			case swept:
				note = noBottle
			}
			note += strings.TrimRight(haveNote(rows[i], firstVersion(p)), " ")
		}
		fmt.Fprintf(w, "%s%s%-44s %s\r\n", cursor, mark, rows[i], note)
	}
	fmt.Fprintf(w, "\r\n%s\r\n", b.status())
}

func (b *browser) status() string {
	if b.message != "" {
		m := b.message
		b.message = ""
		return m
	}
	var keys []string
	for _, k := range browseKeys {
		keys = append(keys, k.key+" "+k.does)
	}
	return strings.Join(keys, " · ")
}

// Seams: a test must not need a terminal, and CI has none.
var (
	termIsTerminal = term.IsTerminal
	termMakeRaw    = term.MakeRaw
	termRestore    = term.Restore
	termGetSize    = term.GetSize
)
