package main

import (
	"bytes"
	"strings"
	"testing"
)

// WHO NEEDS THIS. The question `ls --tree` cannot answer however deep it
// goes, and the one asked before CHANGING something.
func TestLsDependentsWalksTheOtherWay(t *testing.T) {
	withCatalog(t, browseCat(), nil)
	var out, errb bytes.Buffer
	if code := runLs([]string{"--dependents", "zlib.net"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "curl.se") {
		t.Errorf("curl.se needs zlib and is not listed:\n%s", got)
	}
	// AND NOT THE FORWARD ANSWER. zlib declares nothing, so a command that
	// quietly ran DepTree instead would print an empty list and look
	// plausible — which is how a flag that does nothing survives.
	if strings.Contains(got, "declares no runtime dependencies") {
		t.Errorf("--dependents printed the forward answer:\n%s", got)
	}
	// THE LIMIT IS PRINTED WHERE THE NUMBER IS READ. A runtime closure is
	// not a rebuild set, and a reader who takes one for the other plans
	// the wrong work.
	if !strings.Contains(got, "runtime dependents") || !strings.Contains(got, "build-only") {
		t.Errorf("the answer does not say what it is not:\n%s", got)
	}
}

// A project nothing needs is not an error, and the sentence says the
// catalogue was read rather than leaving a blank that could mean anything.
func TestLsDependentsOfALeafSaysSo(t *testing.T) {
	withCatalog(t, browseCat(), nil)
	var out, errb bytes.Buffer
	if code := runLs([]string{"--dependents", "curl.se"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "nothing in this catalogue needs it") {
		t.Errorf("a project nobody needs printed:\n%s", out.String())
	}
}

// "Who needs nothing in particular" has no answer, and the whole catalogue
// printed sideways is not one.
func TestLsDependentsNeedsAProject(t *testing.T) {
	withCatalog(t, browseCat(), nil)
	var out, errb bytes.Buffer
	if code := runLs([]string{"--dependents"}, &out, &errb); code != 2 {
		t.Errorf("code=%d, want 2; out=%s err=%s", code, out.String(), errb.String())
	}
}

// NIX'S QUESTION, and its three outcomes are three different exit statuses
// because a caller acts differently on each.
func TestWhyHasThreeOutcomes(t *testing.T) {
	withCatalog(t, browseCat(), nil)

	// A path.
	var out, errb bytes.Buffer
	if code := runWhy([]string{"curl.se", "zlib.net"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "curl.se") || !strings.Contains(out.String(), "zlib.net") {
		t.Errorf("the path is missing an end:\n%s", out.String())
	}

	// NO path — an answer, not a failure to look. It is what tells somebody
	// a bump cannot reach them, so it goes to stdout and says the words.
	out.Reset()
	errb.Reset()
	if code := runWhy([]string{"zlib.net", "curl.se"}, &out, &errb); code != 1 {
		t.Errorf("code=%d, want 1", code)
	}
	if !strings.Contains(out.String(), "does not need") {
		t.Errorf("a missing path printed:\n%s", out.String())
	}

	// A NAME THAT IS NOT THERE is a different thing from no path, and
	// merging the two sends the reader hunting for a dependency that was
	// never spelt right.
	out.Reset()
	errb.Reset()
	if code := runWhy([]string{"nope.invalid", "zlib.net"}, &out, &errb); code != 1 {
		t.Errorf("code=%d, want 1", code)
	}
	if !strings.Contains(errb.String(), "not in this catalogue") {
		t.Errorf("an unknown project was reported as %q / %q", out.String(), errb.String())
	}
}

func TestWhyWantsTwoArguments(t *testing.T) {
	withCatalog(t, browseCat(), nil)
	for _, args := range [][]string{{}, {"curl.se"}, {"a", "b", "c"}} {
		var out, errb bytes.Buffer
		if code := runWhy(args, &out, &errb); code != 2 {
			t.Errorf("runWhy(%v) = %d, want 2", args, code)
		}
	}
}
