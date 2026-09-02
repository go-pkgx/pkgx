package main

// The module front-end: `pkge load/unload/purge/list/save/restore`, an
// Lmod-shaped command over pkgx's own resolution.
//
// The design decision that makes it exact: state lives in the ENVIRONMENT and
// is read back by pkgx, not by the shell.
//
//   PKGE_SPECS  the package set currently loaded, space-separated
//   PKGE_BASE   the ORIGINAL value of every variable pkgx has touched,
//                  base64 of NUL-separated NAME=value records
//
// The records are separated by NUL, not by a newline. base64 protects the
// transport, not the FRAMING: a variable's value may legitimately contain a
// newline, and a newline-framed record for it decoded back as a truncated
// value — caught by a test, not by reading. NUL cannot occur in an environment
// value, since the kernel hands them over NUL-terminated.
//
// Loading never modifies incrementally: it restores the base and recomputes the
// whole environment from the whole spec set. Unloading is therefore exact and
// ORDER-INDEPENDENT — `unload a` after `load a b c` leaves precisely what
// `load b c` would have, which an incremental implementation cannot promise.
// Lmod carries a module table for the same reason; this carries the base
// instead, which is smaller and cannot disagree with itself.
//
// A variable's base is captured ONCE, the first time something touches it: the
// second load must not record the first load's output as the pristine value.

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// modState is the front-end's state, decoded from the environment.
type modState struct {
	specs []string
	base  map[string]string // variable -> value before pkgx first touched it
	// had records whether the variable EXISTED before. Restoring a variable that
	// was previously unset means unsetting it, not setting it empty: an empty
	// PATH and no PATH are different to every program that reads one.
	had map[string]bool
}

// loadModState reads PKGE_SPECS and PKGE_BASE from the environment.
func loadModState(getenv func(string) string) modState {
	st := modState{base: map[string]string{}, had: map[string]bool{}}
	st.specs = strings.Fields(getenv("PKGE_SPECS"))
	raw, err := base64.StdEncoding.DecodeString(getenv("PKGE_BASE"))
	if err != nil {
		return st // unreadable state is treated as none: the next load recaptures
	}
	for _, line := range strings.Split(string(raw), "\x00") {
		name, value, ok := strings.Cut(line, "=")
		if !ok || name == "" {
			continue
		}
		if strings.HasSuffix(name, "!") { // "NAME!" marks a variable that was UNSET
			st.base[strings.TrimSuffix(name, "!")] = ""
			st.had[strings.TrimSuffix(name, "!")] = false
			continue
		}
		st.base[name] = value
		st.had[name] = true
	}
	return st
}

// encode renders the state's base for PKGE_BASE.
func (st modState) encode() string {
	names := make([]string, 0, len(st.base))
	for n := range st.base {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		if !st.had[n] {
			fmt.Fprintf(&b, "%s!=\x00", n)
			continue
		}
		fmt.Fprintf(&b, "%s=%s\x00", n, st.base[n])
	}
	return base64.StdEncoding.EncodeToString([]byte(b.String()))
}

// capture records the pristine value of every variable in names that the state
// has not already recorded.
func (st *modState) capture(names []string, getenv func(string) (string, bool)) {
	for _, n := range names {
		if _, seen := st.base[n]; seen {
			continue
		}
		v, ok := getenv(n)
		st.base[n], st.had[n] = v, ok
	}
}

// restoreScript emits the shell that puts every captured variable back.
func (st modState) restoreScript(w io.Writer) {
	names := make([]string, 0, len(st.base))
	for n := range st.base {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !st.had[n] {
			fmt.Fprintf(w, "unset %s\n", n)
			continue
		}
		fmt.Fprintf(w, "export %s=%s\n", n, shellQuote(st.base[n]))
	}
}

// shellQuote renders a value as a single-quoted POSIX shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellInit is the front-end itself: a shell function, printed for
// `eval "$(pkgx --shell-init)"` in a profile. It is deliberately thin — every
// decision is made by pkgx, which can be tested; the function only evaluates
// what pkgx prints.
const shellInit = `pkge() {
  _pkge_cmd="${1-}"; [ $# -gt 0 ] && shift
  case "$_pkge_cmd" in
    load|unload|purge)
      _pkge_out=$(command pkgx --shell-"$_pkge_cmd" "$@") || return $?
      eval "$_pkge_out" ;;
    list)
      # shellcheck disable=SC2086
      [ -n "${PKGE_SPECS-}" ] && printf '%s\n' ${PKGE_SPECS} ;;
    save)
      [ -n "${1-}" ] || { echo "pkge save <name>" >&2; return 2; }
      mkdir -p "${PKGX_DIR:-$HOME/.pkgx}/collections" &&
      printf '%s\n' "${PKGE_SPECS-}" > "${PKGX_DIR:-$HOME/.pkgx}/collections/$1" ;;
    restore)
      [ -n "${1-}" ] || { echo "pkge restore <name>" >&2; return 2; }
      _pkge_f="${PKGX_DIR:-$HOME/.pkgx}/collections/$1"
      [ -r "$_pkge_f" ] || { echo "pkge: no collection $1" >&2; return 1; }
      pkge purge || return $?
      # shellcheck disable=SC2046
      pkge load $(cat "$_pkge_f") ;;
    ""|help|-h|--help)
      echo "usage: pkge load|unload <pkg>... | purge | list | save|restore <name>" >&2 ;;
    *)
      echo "pkge: unknown command $_pkge_cmd" >&2; return 2 ;;
  esac
}
`

// modeShellInit prints the shell function.
func modeShellInit(stdout io.Writer) error {
	_, err := io.WriteString(stdout, shellInit)
	return err
}

// modeShell emits the shell for load / unload / purge.
//
// All three go through the same path: work out the resulting spec set, restore
// the base, and recompute. `purge` is simply the empty set.
func modeShell(action string, args []string, compose func([]string) (composed, error), getenv func(string) (string, bool), stdout io.Writer) error {
	get1 := func(n string) string { v, _ := getenv(n); return v }
	st := loadModState(get1)

	var specs []string
	switch action {
	case "load":
		specs = append(append([]string{}, st.specs...), args...)
		specs = dedupeSpecs(specs)
	case "unload":
		drop := map[string]bool{}
		for _, a := range args {
			drop[a] = true
		}
		for _, s := range st.specs {
			// Named either as given or by project alone: `unload gnu.org/bash`
			// must work after `load gnu.org/bash@5`, or nobody can unload what
			// they loaded with a constraint.
			if drop[s] || drop[project(s)] {
				continue
			}
			specs = append(specs, s)
		}
	case "purge":
		specs = nil
	}

	var c composed
	if len(specs) > 0 {
		var err error
		if c, err = compose(specs); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(c.Env))
	for _, e := range c.Env {
		names = append(names, e.Name)
	}
	st.capture(names, getenv)

	// Restore FIRST, then apply: the composition is computed against the
	// pristine values, so a second load does not stack on the first one's output.
	st.restoreScript(stdout)
	for _, e := range c.Env {
		if e.Value != "" {
			fmt.Fprintf(stdout, "export %s=%s\n", e.Name, shellQuote(e.Value))
			continue
		}
		fmt.Fprintf(stdout, "export %s=%s\n", e.Name, shellQuote(strings.Join(e.Prepend, ":"))+`"${`+e.Name+`:+:${`+e.Name+`}}"`)
	}
	if len(specs) == 0 {
		// Nothing loaded: the state goes too, so a later load captures a fresh
		// base rather than one recorded in a previous life of this shell.
		fmt.Fprintln(stdout, "unset PKGE_SPECS")
		fmt.Fprintln(stdout, "unset PKGE_BASE")
		return nil
	}
	fmt.Fprintf(stdout, "export PKGE_SPECS=%s\n", shellQuote(strings.Join(specs, " ")))
	fmt.Fprintf(stdout, "export PKGE_BASE=%s\n", shellQuote(st.encode()))
	return nil
}

// dedupeSpecs keeps the LAST mention of each project: `load node@22` after
// `load node@20` is a swap, which is what a user means by it.
func dedupeSpecs(in []string) []string {
	seen := map[string]int{}
	var out []string
	for _, s := range in {
		if i, ok := seen[project(s)]; ok {
			out[i] = s
			continue
		}
		seen[project(s)] = len(out)
		out = append(out, s)
	}
	return out
}

// osLookupEnv is a seam so tests drive the environment without setting it.
var osLookupEnv = os.LookupEnv
