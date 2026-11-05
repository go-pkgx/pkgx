package app

import (
	"strings"
	"testing"

	"github.com/go-zpkg/bottle"
)

// ⛔⛔ A HELP TEXT THAT NAMES A DEFAULT MUST NAME THE DEFAULT.
//
// Both usages printed `oci://ghcr.io/go-zpkg/packages` as the ZPKG_DIST
// default while `bottle.DistBase` was `oci://ghcr.io/go-pkgx/packages`. The
// go-pkgx → go-zpkg rename rewrote the prose and could not rewrite the
// constant, because the constant is a deployment fact: GitHub's container
// packages are org-scoped and did not follow the repository transfer, so
// every bottle stayed where it was.
//
// Nothing could see the disagreement. `zpkgx -h` is a string, the resolver
// reads a var, and the two only meet in a reader who happens to compare them.
// A person following the printed default would point ZPKG_DIST at an empty
// registry and read the 403 as a permissions problem — the exact confusion
// `request.go` exists to clear up.
//
// So the two are compared here. This does not say which value is right; it
// says they cannot differ, which is the part that was silently false.
func TestEveryHelpTextNamesTheRealRegistryDefault(t *testing.T) {
	// The prose writes the URL as the user would type it into ZPKG_DIST,
	// which is exactly DistBase's own spelling, scheme included.
	want := bottle.DistBase
	if want == "" {
		t.Fatal("bottle.DistBase is empty: this test would then pass on any help text at all")
	}
	for _, tc := range []struct{ what, text string }{
		{"zpkgx usage", usage},
		{"zpkgs usage", zpkgsUsage},
	} {
		if !strings.Contains(tc.text, "ZPKG_DIST") {
			t.Errorf("%s does not document ZPKG_DIST at all", tc.what)
			continue
		}
		if !strings.Contains(tc.text, want) {
			t.Errorf("%s does not name the real default %q — a reader who follows it "+
				"points ZPKG_DIST at a registry that is not the one this binary uses",
				tc.what, want)
		}
	}
}

// ⛔ AND NO HELP TEXT MAY NAME A REGISTRY THAT IS NOT THE DEFAULT AS IF IT
// WERE ONE. The stale value is still a perfectly good-looking URL, so a reader
// cannot tell it apart; naming it here is what makes the next rename stop.
func TestNoHelpTextNamesTheEmptyRegistry(t *testing.T) {
	const empty = "ghcr.io/go-zpkg/packages"
	if strings.Contains(bottle.DistBase, empty) {
		t.Skip("DistBase now points there, so the bottles were copied and this check is obsolete")
	}
	for _, tc := range []struct{ what, text string }{
		{"zpkgx usage", usage},
		{"zpkgs usage", zpkgsUsage},
	} {
		if strings.Contains(tc.text, empty) {
			t.Errorf("%s names %s, which holds no bottles — probed ABSENT 2026-11-04 "+
				"against gnu.org/bash as a present control", tc.what, empty)
		}
	}
}
