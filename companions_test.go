package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/go-pkgx/bottle"
)

// stubCompanions replaces the two questions addCompanions asks, and restores
// them. `avail` is the set of projects the registry can actually serve.
func stubCompanions(t *testing.T, recipes map[string]map[string]string, avail map[string]bool, readErr map[string]error) {
	t.Helper()
	oc, op, ow := companionsFor, pickVersionFor, bottle.Warn
	t.Cleanup(func() { companionsFor, pickVersionFor, bottle.Warn = oc, op, ow })
	companionsFor = func(project, _, _ string) (map[string]string, error) {
		if err := readErr[project]; err != nil {
			return nil, err
		}
		return recipes[project], nil
	}
	pickVersionFor = func(project, _, _, _ string) (bottle.Ver, error) {
		if !avail[project] {
			return bottle.Ver{}, errors.New("no bottle")
		}
		return bottle.Ver{}, nil
	}
}

// rust-lang.org ships bin/cargo-clippy and bin/cargo-fmt and NO `cargo` —
// cargo is the separate rust-lang.org/cargo project it names as a companion.
// A recipe declaring rust-lang.org and running `cargo` died with
// `"cargo": executable file not found in $PATH` until the key was read.
func TestCompanionOfANamedRootIsAdded(t *testing.T) {
	stubCompanions(t,
		map[string]map[string]string{"rust-lang.org": {"rust-lang.org/cargo": "*"}},
		map[string]bool{"rust-lang.org": true, "rust-lang.org/cargo": true}, nil)

	roots := map[string]string{"rust-lang.org": "^1.56"}
	addCompanions(roots)
	want := map[string]string{"rust-lang.org": "^1.56", "rust-lang.org/cargo": "*"}
	if !reflect.DeepEqual(roots, want) {
		t.Errorf("roots = %v, want %v", roots, want)
	}
}

// A companion with no bottle for this platform is an ANSWER — we looked, there
// is nothing to install — so it is skipped, and skipped quietly.
func TestUnavailableCompanionIsSkippedQuietly(t *testing.T) {
	stubCompanions(t,
		map[string]map[string]string{"a.org": {"b.org/extra": "*"}},
		map[string]bool{"a.org": true}, nil)
	var warned []string
	bottle.Warn = func(m string) { warned = append(warned, m) }

	roots := map[string]string{"a.org": "*"}
	addCompanions(roots)
	if _, ok := roots["b.org/extra"]; ok {
		t.Errorf("added a companion with no bottle: %v", roots)
	}
	if len(warned) != 0 {
		t.Errorf("warned about an expected absence: %v", warned)
	}
}

// Failing to READ the recipe is not an answer. Treating "I could not look" as
// "there are none" is the defect this whole change fixes, so it says so.
func TestUnreadableCompanionsWarn(t *testing.T) {
	stubCompanions(t, nil, map[string]bool{"a.org": true}, map[string]error{"a.org": errors.New("404")})
	var warned []string
	bottle.Warn = func(m string) { warned = append(warned, m) }

	roots := map[string]string{"a.org": "*"}
	addCompanions(roots)
	if len(warned) != 1 {
		t.Fatalf("warnings = %v, want one", warned)
	}
	if len(roots) != 1 {
		t.Errorf("roots changed on an unreadable recipe: %v", roots)
	}
}

// Named roots only. Following a companion's own companions would walk a
// suggestion graph of unbounded width for packages nobody asked for.
func TestCompanionsOfCompanionsAreNotFollowed(t *testing.T) {
	stubCompanions(t,
		map[string]map[string]string{
			"a.org": {"b.org": "*"},
			"b.org": {"c.org": "*"},
		},
		map[string]bool{"a.org": true, "b.org": true, "c.org": true}, nil)

	roots := map[string]string{"a.org": "*"}
	addCompanions(roots)
	if _, ok := roots["b.org"]; !ok {
		t.Errorf("the named root's companion is missing: %v", roots)
	}
	if _, ok := roots["c.org"]; ok {
		t.Errorf("followed a companion's companion: %v", roots)
	}
}

// A constraint the caller asked for outranks a companion's `*`.
func TestCompanionDoesNotOverwriteAnAskedForConstraint(t *testing.T) {
	stubCompanions(t,
		map[string]map[string]string{"a.org": {"b.org": "*"}},
		map[string]bool{"a.org": true, "b.org": true}, nil)

	roots := map[string]string{"a.org": "*", "b.org": "^2"}
	addCompanions(roots)
	if roots["b.org"] != "^2" {
		t.Errorf("b.org = %q, want the caller's ^2", roots["b.org"])
	}
}

// A root that is not a project at all — `pkgx ./x.py` — must not be reported as
// a companions problem. The resolver rejects the name properly a moment later;
// leading with "could not read ./x.py's companions" blames the wrong thing.
func TestUnresolvableRootIsSilentHere(t *testing.T) {
	stubCompanions(t, nil, map[string]bool{}, map[string]error{"./x.py": errors.New("Not Found")})
	var warned []string
	bottle.Warn = func(m string) { warned = append(warned, m) }

	roots := map[string]string{"./x.py": "*"}
	addCompanions(roots)
	if len(warned) != 0 {
		t.Errorf("warned about a name that is not a project: %v", warned)
	}
}
