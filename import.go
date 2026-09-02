package main

// Converting a site's modulefiles — Lmod's Lua and Environment Modules' TCL —
// into the HCL2 environments this toolchain loads.
//
// It is a PARSER, not an evaluator, and that is the whole design.
//
// An interpreter runs a modulefile and silently skips what it does not
// implement: a `depends_on`, a `family`, a branch on `mode()`, a hierarchical
// MODULEPATH. The result is not an error, it is an environment that is subtly
// wrong — discovered inside somebody's job, days later. A converter can only
// succeed on a file whose meaning is UNAMBIGUOUS: every statement it does not
// recognise, and every argument that is not a literal, stops the conversion and
// is named with its line. That failure lands in front of the person doing the
// migration, which is the only moment anyone can act on it.
//
// So: convert what is certain, refuse the rest OUT LOUD, and never emit a
// partial conversion — a half-converted environment is the one outcome nobody
// can check.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// modImport is what one modulefile turned into.
type modImport struct {
	Name        string
	Description string
	Source      string
	Prepend     map[string][]string
	Append      map[string][]string
	Setenv      map[string]string
	Unsetenv    []string
}

// refusal is one statement the converter would have had to guess at.
type refusal struct {
	Line int
	Text string
	Why  string
}

func (r refusal) String() string {
	return fmt.Sprintf("  line %d: %s\n    %s", r.Line, r.Why, r.Text)
}

var (
	// A Lua statement this converter accepts is a bare call: verb(args). Not an
	// assignment, not a conditional, not a chained expression.
	//
	// (?s) so `.` matches a newline: a help([[ … ]]) block spans lines and the
	// scanner joins them into one statement before this runs. Without it the
	// joined statement matched nothing and every real modulefile was refused for
	// its help text — caught by a fixture, not by reading the regexp.
	luaCall = regexp.MustCompile(`(?s)^([a-zA-Z_][a-zA-Z0-9_]*)\s*\((.*)\)\s*$`)
	// A TCL statement is a verb followed by words.
	tclStmt = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9-]*)\s*(.*)$`)
	// A Lua string literal, single or double quoted.
	luaString = regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"$|^'((?:[^'\\]|\\.)*)'$`)
	// A Lua long bracket, [[…]] or [==[…]==]. Modulefiles use it for help text,
	// almost always across several lines — which is why the scanner joins those
	// lines before parsing rather than refusing them: a long bracket IS a
	// literal, and refusing it would refuse nearly every real modulefile for a
	// string that changes no environment.
	luaLongOpen  = regexp.MustCompile(`\[(=*)\[`)
	luaLongWhole = regexp.MustCompile(`(?s)^\[(=*)\[(.*)\]=*\]$`)
)

// importModulefile converts one file, returning every statement it refused.
func importModulefile(path string, r io.Reader) (modImport, []refusal, error) {
	m := modImport{
		Name:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Source:  path,
		Prepend: map[string][]string{},
		Append:  map[string][]string{},
		Setenv:  map[string]string{},
	}
	lua := strings.HasSuffix(path, ".lua")
	var refusals []refusal
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	// n is the line the statement STARTED on, so a refusal points at the line a
	// person will look for, not at the end of a joined block.
	for n := 0; ; {
		line, start, more := nextStatement(sc, lua, &n)
		if !more {
			break
		}
		_ = start
		// #%Module is the TCL magic first line; -- and # are comments.
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "#") {
			continue
		}
		verb, args, why := splitStatement(line, lua)
		if why != "" {
			refusals = append(refusals, refusal{start, oneLine(line), why})
			continue
		}
		if why := m.apply(verb, args); why != "" {
			refusals = append(refusals, refusal{start, oneLine(line), why})
		}
	}
	if err := sc.Err(); err != nil {
		return modImport{}, nil, err
	}
	return m, refusals, nil
}

// nextStatement returns the next logical statement: one line, unless a Lua long
// bracket opened and did not close, in which case the lines are joined until it
// does. It skips blanks and comments. start is the line the statement began on.
func nextStatement(sc *bufio.Scanner, lua bool, n *int) (stmt string, start int, more bool) {
	for sc.Scan() {
		*n++
		line := strings.TrimSpace(sc.Text())
		// #%Module is the TCL magic first line; -- and # are comments.
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "#") {
			continue
		}
		start = *n
		if !lua {
			return line, start, true
		}
		for openUnclosedLongBracket(line) && sc.Scan() {
			*n++
			line += "\n" + strings.TrimSpace(sc.Text())
		}
		return line, start, true
	}
	return "", 0, false
}

// openUnclosedLongBracket reports whether the line opens a Lua long bracket it
// does not close.
func openUnclosedLongBracket(line string) bool {
	m := luaLongOpen.FindStringSubmatchIndex(line)
	if m == nil {
		return false
	}
	eq := line[m[2]:m[3]]
	return !strings.Contains(line[m[1]:], "]"+eq+"]")
}

// oneLine collapses a joined statement for the refusal message, so a fifty-line
// help block does not become a fifty-line error.
func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// splitStatement pulls a verb and its LITERAL arguments out of one line, or
// says why it will not.
func splitStatement(line string, lua bool) (verb string, args []string, why string) {
	if lua {
		mm := luaCall.FindStringSubmatch(line)
		if mm == nil {
			return "", nil, "not a plain call: this converter reads statements, it does not run Lua"
		}
		verb = mm[1]
		if strings.TrimSpace(mm[2]) == "" {
			return verb, nil, ""
		}
		for _, a := range splitArgs(mm[2]) {
			s, ok := luaLiteral(a)
			if !ok {
				return "", nil, "argument is not a string literal (" + strings.TrimSpace(a) + "): its value depends on something we are not running"
			}
			args = append(args, s)
		}
		return verb, args, ""
	}
	mm := tclStmt.FindStringSubmatch(line)
	if mm == nil {
		return "", nil, "not a plain statement: this converter reads statements, it does not run TCL"
	}
	verb = mm[1]
	for _, a := range tclWords(mm[2]) {
		// $var, [exec …] and {a b} all mean the value is computed elsewhere.
		if strings.ContainsAny(a, "$[]{}") {
			return "", nil, "argument is not a literal (" + a + "): its value depends on something we are not running"
		}
		args = append(args, strings.Trim(a, `"`))
	}
	return verb, args, ""
}

// tclWords splits a TCL argument list, keeping a double-quoted run together so
// `module-whatis "the solver stack"` arrives as one word.
func tclWords(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			inQuote = !inQuote
		case (c == ' ' || c == '\t') && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// splitArgs splits a Lua argument list on top-level commas.
func splitArgs(s string) []string {
	var out []string
	depth, quote := 0, byte(0)
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '{' || c == '[':
			depth++
		case c == ')' || c == '}' || c == ']':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// luaLiteral unquotes a Lua string literal, or reports that it is not one.
func luaLiteral(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if mm := luaLongWhole.FindStringSubmatch(s); mm != nil {
		return strings.TrimSpace(mm[2]), true
	}
	mm := luaString.FindStringSubmatch(s)
	if mm == nil {
		return "", false
	}
	if strings.HasPrefix(s, `"`) {
		return unescape(mm[1]), true
	}
	return unescape(mm[2]), true
}

func unescape(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(s)
}

// apply records one recognised statement, or says why it is refused.
//
// The refusals are not a gap to fill later. `conflict`, `family` and
// `depends_on` are RELATIONSHIPS between modules, and `load`/`prereq` pull in
// another modulefile: honouring them would mean deciding what a whole tree
// means, which is precisely the interpreter this converter exists not to be.
func (m *modImport) apply(verb string, args []string) string {
	switch verb {
	case "prepend_path", "prepend-path":
		if len(args) < 2 {
			return "prepend_path needs a variable and a value"
		}
		m.Prepend[args[0]] = append(m.Prepend[args[0]], args[1])
	case "append_path", "append-path":
		if len(args) < 2 {
			return "append_path needs a variable and a value"
		}
		m.Append[args[0]] = append(m.Append[args[0]], args[1])
	case "setenv":
		if len(args) < 2 {
			return "setenv needs a variable and a value"
		}
		m.Setenv[args[0]] = args[1]
	case "unsetenv":
		if len(args) < 1 {
			return "unsetenv needs a variable"
		}
		m.Unsetenv = append(m.Unsetenv, args[0])
	case "whatis", "module-whatis":
		if m.Description == "" {
			m.Description = strings.Join(args, " ")
		}
	case "help", "module-help":
		// Prose for `module help`. It changes no environment.
	case "conflict", "family", "depends_on", "prereq", "load", "module", "sticky", "add_property", "always_load":
		return verb + " states a RELATIONSHIP between modules, not an environment change: decide it in the environment that replaces this one"
	default:
		return "unknown statement " + verb
	}
	return ""
}

// writeHCL renders one conversion as an HCL2 env block.
func writeHCL(m modImport, w io.Writer) {
	fmt.Fprintf(w, "# imported from %s by `pkgx env import`.\n", m.Source)
	fmt.Fprintln(w, "# Every statement in that file was recognised; nothing was skipped.")
	fmt.Fprintf(w, "env %q {\n", m.Name)
	if m.Description != "" {
		fmt.Fprintf(w, "  description = %q\n", m.Description)
	}
	writeMapOfLists(w, "prepend_path", m.Prepend)
	writeMapOfLists(w, "append_path", m.Append)
	if len(m.Setenv) > 0 {
		fmt.Fprintln(w, "  setenv = {")
		for _, k := range sortedMapKeys(m.Setenv) {
			fmt.Fprintf(w, "    %s = %q\n", k, m.Setenv[k])
		}
		fmt.Fprintln(w, "  }")
	}
	if len(m.Unsetenv) > 0 {
		fmt.Fprint(w, "  unsetenv = [")
		for i, v := range m.Unsetenv {
			if i > 0 {
				fmt.Fprint(w, ", ")
			}
			fmt.Fprintf(w, "%q", v)
		}
		fmt.Fprintln(w, "]")
	}
	fmt.Fprintln(w, "}")
}

func writeMapOfLists(w io.Writer, name string, m map[string][]string) {
	if len(m) == 0 {
		return
	}
	fmt.Fprintf(w, "  %s = {\n", name)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "    %s = [", k)
		for i, v := range m[k] {
			if i > 0 {
				fmt.Fprint(w, ", ")
			}
			fmt.Fprintf(w, "%q", v)
		}
		fmt.Fprintln(w, "]")
	}
	fmt.Fprintln(w, "  }")
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// modeImport converts the named modulefiles, writing HCL2 to stdout — and
// writing NOTHING if any file carried a statement it would have had to guess at.
func modeImport(paths []string, stdout, stderr io.Writer) error {
	var out strings.Builder
	bad := 0
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		m, refusals, err := importModulefile(p, f)
		f.Close()
		if err != nil {
			return err
		}
		if len(refusals) > 0 {
			bad++
			fmt.Fprintf(stderr, "%s: %d statement(s) this converter will not guess at:\n", p, len(refusals))
			for _, r := range refusals {
				fmt.Fprintln(stderr, r)
			}
			continue
		}
		writeHCL(m, &out)
		out.WriteString("\n")
	}
	if bad > 0 {
		return fmt.Errorf("%d of %d modulefile(s) not converted — nothing written, because a PARTIAL conversion is the one outcome nobody can check", bad, len(paths))
	}
	_, err := io.WriteString(stdout, out.String())
	return err
}
