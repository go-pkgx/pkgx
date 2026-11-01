package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgx/bottle"
)

// TestTheListIsActivated is the whole point of the file under test. bottle has
// carried the ranking since v0.61.0 and it changed no answer for anybody,
// because the only source was an environment variable nobody sets.
func TestTheListIsActivated(t *testing.T) {
	if len(bottle.DefaultDeprecated) == 0 {
		t.Fatal("bottle.DefaultDeprecated is empty, so the EOL ranking is inert again")
	}
	if got := bottle.DefaultDeprecated["openssl.org"]; len(got) == 0 {
		t.Error("no openssl.org lines are deprecated, which is what this activation is for")
	}
}

// ⛔ TestNoSupportedLineIsDeprecated is the test that earns supportedLines.
// Listing a line that is still supported is the expensive mistake: it would
// push `^3` off the 3.5 LTS onto something older, quietly, with no error
// anywhere, and the only symptom would be a version nobody chose.
func TestNoSupportedLineIsDeprecated(t *testing.T) {
	for project, supported := range supportedLines {
		dead := deprecatedLines[project]
		for _, line := range supported {
			if slices.Contains(dead, line) {
				t.Errorf("%s line %s is recorded both supported and deprecated; one of the two halves is wrong", project, line)
			}
		}
	}
}

// TestEveryProjectRecordsBothHalves: a project with deprecated lines and no
// supported ones is either a project we should stop publishing or a
// half-finished edit. Either way the measurement is incomplete, and an
// incomplete measurement is what the previous test relies on being complete.
func TestEveryProjectRecordsBothHalves(t *testing.T) {
	for project := range deprecatedLines {
		if len(supportedLines[project]) == 0 {
			t.Errorf("%s lists deprecated lines but no supported one — if NO line is supported, say so here deliberately", project)
		}
	}
	for project := range supportedLines {
		if _, ok := deprecatedLines[project]; !ok {
			t.Errorf("%s lists supported lines but no deprecated one; drop it or record the verdict", project)
		}
	}
}

// TestLinesAreLinesAndNotVersions. bottle matches a line as a prefix on a
// component boundary, so "3.6" holds 3.6.4 and not 3.60. A full version here
// would still match itself and read as if the whole line were covered, which
// is the kind of entry that looks right in review and covers one release.
func TestLinesAreLinesAndNotVersions(t *testing.T) {
	for project, lines := range deprecatedLines {
		for _, line := range lines {
			switch {
			case line == "":
				t.Errorf("%s has an empty line, which bottle skips — so it silently covers nothing", project)
			case strings.HasPrefix(line, "."), strings.HasSuffix(line, "."):
				t.Errorf("%s line %q is not a dotted prefix", project, line)
			case strings.Count(line, ".") > 2:
				t.Errorf("%s line %q has four components, so it names a release, not a line", project, line)
			}
		}
	}
}

// TestAsOfIsADate: the date is the only thing that distinguishes a current
// judgement from one a release left behind, and a date that does not parse is
// no date at all. In the future it is a typo, not a measurement.
func TestAsOfIsADate(t *testing.T) {
	d, err := time.Parse(time.DateOnly, deprecatedAsOf)
	if err != nil {
		t.Fatalf("deprecatedAsOf = %q: %v", deprecatedAsOf, err)
	}
	if d.After(time.Now().UTC().AddDate(0, 0, 1)) {
		t.Errorf("deprecatedAsOf %s is in the future", deprecatedAsOf)
	}
}
