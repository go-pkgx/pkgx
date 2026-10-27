package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-pkgx/bottle"
)

// A local record of what did not work.
//
// # WHY IT IS LOCAL, AND WHY THAT IS THE WHOLE DESIGN
//
// The useful signal from a package manager is which packages people reach
// for and do not get: a missing platform, a constraint nothing satisfies, a
// soname no bottle provides. Upstream package managers collect that by
// phoning home. This does not, and the reason is already written into the
// command it sits beside — `pkgs request` prints a report you can open as an
// issue and says, in its own help, that it *sends nothing*.
//
// So this keeps the same promise and adds the only thing `request` cannot:
// memory. `request` answers about one project at the moment you ask. The
// journal answers "what has been failing here, and how often" — which is the
// question worth acting on, and the one a single error message cannot reach.
//
// Nothing here opens a socket. `pkgs journal` prints the file; turning it
// into a report is `pkgs request`, which still sends nothing by itself.
//
// # THREE THINGS IT MUST NOT DO
//
//   - It must not break the command it is recording. A diary that cannot be
//     written is not a reason for `pkgx node` to fail, so every error here is
//     dropped on purpose.
//   - It must not grow without bound. A failing command in a loop would
//     otherwise fill the disk, which is a denial of service a package manager
//     hands to its own user. One rotation, bounded at 2×maxJournal.
//   - It must not carry what it cannot print. The text comes from error
//     strings that quote names out of a catalogue or a registry, so it is
//     sanitised on the way IN: what reaches the file is already safe to show.
const (
	maxJournal = 256 << 10 // bytes before the file rotates
	maxField   = 400       // bytes of any one recorded field
)

// entry is one failure, one JSON object per line: appendable without reading,
// readable without a parser of our own, and skippable line-by-line if one is
// ever truncated by a crash mid-write.
type entry struct {
	When    time.Time `json:"when"`
	Kind    string    `json:"kind"`
	Project string    `json:"project,omitempty"`
	Message string    `json:"message"`
}

// Kinds. Few on purpose: a kind is for counting, and a taxonomy nobody can
// hold in their head gets one bucket per message.
const (
	kindMissing = "missing" // a package, or a version, this registry does not have
	kindFailed  = "failed"  // anything else that ended the run
)

// journalPath is the file, beside the store the rest of the state lives in.
func journalPath() string { return filepath.Join(bottle.Dir(), "journal.jsonl") }

// journalEnabled is false when PKGX_JOURNAL is set to one of the falsey
// spellings the rest of this program accepts, so a user who wants no local
// record of their failures can have none.
func journalEnabled() bool {
	switch strings.ToLower(os.Getenv("PKGX_JOURNAL")) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// recordFailure appends one entry. It reports nothing: see the contract above.
func recordFailure(err error) {
	if err == nil || !journalEnabled() {
		return
	}
	e := entry{
		When:    time.Now().UTC(),
		Kind:    kindFailed,
		Message: sanitise(err.Error()),
	}
	// bottle already knows how to tell "we do not have this" from "this went
	// wrong", and exports it for the hint `pkgs request` prints. Reusing it
	// means the journal's taxonomy cannot drift from the hint's.
	if p := bottle.ProjectToRequest(err); p != "" {
		e.Kind, e.Project = kindMissing, sanitise(p)
	}
	appendEntry(e)
}

func appendEntry(e entry) {
	line, merr := json.Marshal(e)
	if merr != nil {
		return
	}
	dir := bottle.Dir()
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	rotate(journalPath())
	// O_RDWR, not O_WRONLY: endsMidLine has to READ the last byte. Opened
	// write-only, its ReadAt fails, its "unreadable means intact" fallback
	// returns false, and the repair silently never happens — the guard
	// swallowing the evidence that it is not working.
	f, oerr := os.OpenFile(journalPath(), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if oerr != nil {
		return
	}
	defer f.Close()
	// ⛔ CLOSE A PARTIAL LINE BEFORE ADDING TO IT. A crash mid-write leaves a
	// line with no newline; appending straight onto it would glue the next
	// entry to the damaged one and make BOTH unreadable — one interrupted
	// write costing two records. Found by the test for damaged lines, which
	// recovered one entry where two were expected.
	if endsMidLine(f) {
		_, _ = f.Write([]byte{'\n'})
	}
	_, _ = f.Write(append(line, '\n'))
}

// endsMidLine reports whether the file's last byte is not a newline, i.e. a
// previous write was interrupted. A file we cannot read the end of is
// treated as intact: writing a stray newline into a healthy journal would be
// the worse guess.
func endsMidLine(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return false
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], st.Size()-1); err != nil {
		return false
	}
	return last[0] != '\n'
}

// rotate keeps the journal bounded. The previous file is replaced rather than
// kept as a series: two files is a bound, N files is the same unbounded growth
// with extra steps.
func rotate(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() < maxJournal {
		return
	}
	_ = os.Rename(path, path+".1")
}

// sanitise bounds a field and strips what must not reach a terminal.
//
// ⛔ THE TEXT IS NOT OURS. It is an error string, and error strings here
// quote project names, versions and sonames read out of a catalogue, a
// registry index or a downloaded binary. bottle holds the same line for the
// catalogue it decodes; this is the same rule at the other end, because what
// is written now is printed later by `pkgs journal`.
func sanitise(s string) string {
	if len(s) > maxField {
		s = s[:maxField]
		// Cutting by bytes can leave half a rune; drop the partial tail
		// rather than write a replacement character into the record.
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n', r == '\t':
			b.WriteByte(' ') // one entry is one line
		case r == utf8.RuneError, unicode.IsControl(r), r == 0x7f:
			continue
		case r == '\u200b', r == '\u2028', r == '\u2029':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// readJournal returns the entries, oldest first, skipping any line that is
// not one — a truncated final write is a damaged line, not a damaged file.
func readJournal(path string) []entry {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []entry
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e entry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out
}

// runJournal implements `pkgs journal`.
func runJournal(args []string, stdout, stderr io.Writer) int {
	var clear bool
	n := 20
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--clear":
			clear = true
		case "-n":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "pkgs: -n needs a count")
				return 2
			}
			i++
			if _, err := fmt.Sscan(args[i], &n); err != nil || n < 0 {
				fmt.Fprintf(stderr, "pkgs: -n %q is not a count\n", args[i])
				return 2
			}
		default:
			fmt.Fprintf(stderr, "pkgs: unknown journal option %s (--clear, -n N)\n", args[i])
			return 2
		}
	}

	path := journalPath()
	if clear {
		_ = os.Remove(path)
		_ = os.Remove(path + ".1")
		fmt.Fprintln(stdout, "journal cleared")
		return 0
	}

	entries := readJournal(path)
	if len(entries) == 0 {
		fmt.Fprintf(stdout, "no failures recorded (%s)\n", path)
		if !journalEnabled() {
			fmt.Fprintln(stdout, "PKGX_JOURNAL is off, so nothing is being recorded")
		}
		return 0
	}

	// The counts first: the journal exists to answer "what keeps failing",
	// and a tail of twenty lines does not answer it.
	byProject := map[string]int{}
	missing := 0
	for _, e := range entries {
		if e.Kind == kindMissing {
			missing++
			if e.Project != "" {
				byProject[e.Project]++
			}
		}
	}
	fmt.Fprintf(stdout, "%d failure(s) recorded here, %d of them a package this registry does not have\n",
		len(entries), missing)
	if len(byProject) > 0 {
		fmt.Fprintln(stdout, "\nmost asked for and not here:")
		type pc struct {
			p string
			c int
		}
		pcs := make([]pc, 0, len(byProject))
		for p, c := range byProject {
			pcs = append(pcs, pc{p, c})
		}
		sort.Slice(pcs, func(i, j int) bool {
			if pcs[i].c != pcs[j].c {
				return pcs[i].c > pcs[j].c
			}
			return pcs[i].p < pcs[j].p
		})
		for _, x := range pcs {
			fmt.Fprintf(stdout, "  %4d  %s\n", x.c, x.p)
		}
		fmt.Fprintf(stdout, "\n`%s request <project>` turns one of those into a report you can open as\nan issue. It sends nothing, and neither does this.\n", pkgsTool)
	}

	if n > 0 && len(entries) > 0 {
		fmt.Fprintf(stdout, "\nthe last %d:\n", min(n, len(entries)))
		for _, e := range entries[max(0, len(entries)-n):] {
			fmt.Fprintf(stdout, "  %s  %-7s %s\n", e.When.Format(time.RFC3339), e.Kind, e.Message)
		}
	}
	return 0
}
