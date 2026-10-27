package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/go-pkgx/bottle"
)

// bottleNoBottleErr is the real error bottle raises when a project exists
// but has no bottle for this platform — the one `sayHowToAsk` turns into the
// `pkgs request` hint. Using bottle's own type rather than a lookalike is
// what keeps the journal's taxonomy tied to the hint's.
func bottleNoBottleErr(project string) error {
	return &bottle.NoBottleHereError{Project: project}
}

func journalStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	t.Setenv("PKGX_JOURNAL", "")
	return dir
}

// The gap half: a failure that names a package this registry does not have
// is recorded AS a gap, with the project, so the counts can answer "what do
// people here keep asking for and not get".
func TestJournalRecordsAMissingPackageAsAGap(t *testing.T) {
	journalStore(t)
	recordFailure(bottleNoBottleErr("opendev.org/openstack/python-openstackclient"))

	got := readJournal(journalPath())
	if len(got) != 1 {
		t.Fatalf("want one entry, got %d: %+v", len(got), got)
	}
	if got[0].Kind != kindMissing {
		t.Errorf("kind = %q, want %q", got[0].Kind, kindMissing)
	}
	if got[0].Project != "opendev.org/openstack/python-openstackclient" {
		t.Errorf("project = %q", got[0].Project)
	}
}

// Everything else is a failure, not a gap — the two are counted separately
// because only one of them is a thing anybody can publish.
func TestJournalRecordsOtherFailuresWithoutAProject(t *testing.T) {
	journalStore(t)
	recordFailure(errors.New("something came apart"))
	got := readJournal(journalPath())
	if len(got) != 1 || got[0].Kind != kindFailed || got[0].Project != "" {
		t.Fatalf("got %+v", got)
	}
}

// ⛔ THE JOURNAL MUST NOT BREAK THE COMMAND IT RECORDS. A store that cannot
// be written is a reason for the diary to stay empty, not for `pkgx node` to
// fail, so recordFailure reports nothing and must not panic.
func TestJournalSurvivesAnUnwritableStore(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file-not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PKGX_DIR", filepath.Join(blocked, "store"))
	t.Setenv("PKGX_JOURNAL", "")
	recordFailure(errors.New("boom")) // must simply do nothing
}

// ⛔ IT MUST NOT CARRY WHAT IT CANNOT PRINT. The text is an error string, and
// those quote names read out of a catalogue, a registry index or a
// downloaded binary. What is written now is printed later by `pkgs journal`.
func TestJournalStripsWhatWouldReachATerminal(t *testing.T) {
	journalStore(t)
	recordFailure(errors.New("harmless\x1b[2K\rpkgx  SAFE\x00\x07 and\na newline"))

	got := readJournal(journalPath())
	if len(got) != 1 {
		t.Fatalf("want one entry, got %d", len(got))
	}
	for i, r := range got[0].Message {
		if r == 0x7f || unicode.IsControl(r) {
			t.Fatalf("the record carries %q at %d: %q", r, i, got[0].Message)
		}
	}
	// One entry is one line, so the newline became a space rather than a
	// second record that no reader would attribute correctly.
	if strings.Contains(got[0].Message, "\n") {
		t.Errorf("a newline survived: %q", got[0].Message)
	}
	if !strings.Contains(got[0].Message, "a newline") {
		t.Errorf("the text after the newline was dropped: %q", got[0].Message)
	}
}

// ⛔ IT MUST NOT GROW WITHOUT BOUND. A failing command in a loop would
// otherwise fill the disk — a denial of service a package manager hands to
// its own user. One rotation: bounded at 2x.
func TestJournalIsBounded(t *testing.T) {
	dir := journalStore(t)
	big := strings.Repeat("x", maxJournal+1)
	if err := os.WriteFile(filepath.Join(dir, "journal.jsonl"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	recordFailure(errors.New("the one after the cap"))

	st, err := os.Stat(journalPath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() >= int64(maxJournal) {
		t.Fatalf("the journal did not rotate: %d bytes", st.Size())
	}
	if _, err := os.Stat(journalPath() + ".1"); err != nil {
		t.Fatalf("the rotated file is missing: %v", err)
	}
	// And a second rotation must REPLACE the first, or two files is just
	// unbounded growth with extra steps.
	if err := os.WriteFile(journalPath(), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	recordFailure(errors.New("again"))
	entries, _ := filepath.Glob(journalPath() + ".*")
	if len(entries) != 1 {
		t.Fatalf("want exactly one rotated file, got %v", entries)
	}
}

// A long field is cut, and cut without leaving half a rune behind.
func TestJournalBoundsOneField(t *testing.T) {
	journalStore(t)
	recordFailure(fmt.Errorf("%s", strings.Repeat("é", maxField)))
	got := readJournal(journalPath())
	if len(got) != 1 {
		t.Fatalf("want one entry, got %d", len(got))
	}
	if len(got[0].Message) > maxField {
		t.Errorf("message is %d bytes, cap is %d", len(got[0].Message), maxField)
	}
	if strings.ContainsRune(got[0].Message, '\uFFFD') {
		t.Errorf("the cut left a replacement character: %q", got[0].Message)
	}
}

// Off means off: a user who wants no local record of their failures gets
// none, and the command says so rather than printing an empty list.
func TestJournalCanBeTurnedOff(t *testing.T) {
	journalStore(t)
	t.Setenv("PKGX_JOURNAL", "0")
	recordFailure(errors.New("not recorded"))
	if got := readJournal(journalPath()); len(got) != 0 {
		t.Fatalf("PKGX_JOURNAL=0 still recorded %+v", got)
	}
	var out, errb strings.Builder
	if code := runJournal(nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "PKGX_JOURNAL is off") {
		t.Errorf("the reason for the silence is not stated:\n%s", out.String())
	}
}

// The counts are the point: twenty lines of tail do not answer "what keeps
// failing here", and that is the question the journal exists for.
func TestJournalCountsWhatKeepsFailing(t *testing.T) {
	journalStore(t)
	for i := 0; i < 3; i++ {
		recordFailure(bottleNoBottleErr("wanted.org"))
	}
	recordFailure(bottleNoBottleErr("other.org"))
	recordFailure(errors.New("unrelated"))

	var out, errb strings.Builder
	if code := runJournal(nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	s := out.String()
	for _, want := range []string{
		"5 failure(s)", "4 of them a package this registry does not have",
		"3  wanted.org", "1  other.org", "sends nothing",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the report omits %q:\n%s", want, s)
		}
	}
	// Ordered by how often, so the top line is the one worth acting on.
	if strings.Index(s, "wanted.org") > strings.Index(s, "other.org") {
		t.Errorf("the most-asked-for is not first:\n%s", s)
	}
}

func TestJournalClearRemovesBothFiles(t *testing.T) {
	journalStore(t)
	recordFailure(errors.New("x"))
	if err := os.WriteFile(journalPath()+".1", []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if code := runJournal([]string{"--clear"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, p := range []string{journalPath(), journalPath() + ".1"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived --clear", p)
		}
	}
}

func TestJournalRejectsNonsenseOptions(t *testing.T) {
	journalStore(t)
	for _, args := range [][]string{{"--nope"}, {"-n"}, {"-n", "banana"}, {"-n", "-3"}} {
		var out, errb strings.Builder
		if code := runJournal(args, &out, &errb); code != 2 {
			t.Errorf("runJournal(%v) = %d, want 2", args, code)
		}
	}
}

// A line that is not an entry is skipped, not fatal: a crash mid-write
// damages one line, and losing the file over it would lose the record the
// user is trying to read.
func TestJournalSkipsADamagedLine(t *testing.T) {
	journalStore(t)
	recordFailure(errors.New("first"))
	f, err := os.OpenFile(journalPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"when":"not a ti`)
	f.Close()
	recordFailure(errors.New("third"))

	got := readJournal(journalPath())
	if len(got) != 2 {
		t.Fatalf("want the two good entries, got %d: %+v", len(got), got)
	}
}
