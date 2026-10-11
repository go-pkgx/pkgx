package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-pkgx/bottle"
)

// The name index: the completion path's catalogue, in the form completion
// reads it.
//
// # WHY
//
// A TAB press is a fresh process, so whatever it does is paid again on every
// press. Measured 2026-10-11 on the real linux-aarch64 catalogue — 481,803
// bytes, 1908 projects — reading it the way completion did:
//
//	ReadFile + UnmarshalCatalog          6.3–9.8 ms   ← what every TAB paid
//	ReadFile of the JSON alone            0.31 ms
//	json.Decoder, two fields only         5.1 ms
//	this index (38 KB), read + scan       0.08 ms
//
// So the JSON DECODE is essentially all of it: reading the bytes costs 0.3 ms
// and turning them into a Catalog costs six to nine more. Streaming with
// json.Decoder and decoding only `project` and `versions` is just twice as
// fast, which is not enough to matter.
//
// End to end, in a `FROM scratch` container with the network off (native
// arm64, 200 runs), one completion was 25.25 ms against a 20 ms budget, over a
// 9.37 ms floor that is the Go runtime starting. The decode was the gap.
//
// # WHY A SECOND FILE IS SAFE HERE, AND HOW
//
// A derived file that can disagree with its source is a defect waiting for a
// reader. Three things keep this one honest:
//
//   - It is written by `pkgx catalog update` ONLY, in the same operation that
//     writes the JSON. Nothing else produces it.
//   - Its header records the JSON's BYTE SIZE and the number of projects.
//     Both are checked on read, and both are free — a Stat and a line count.
//     A catalogue that was refreshed without its index fails the check.
//   - A failed check is not an error. It falls back to reading the JSON, which
//     is the authority. The index can only make completion FASTER or make it
//     do what it did before; it cannot make it answer differently.
//
// It carries the project name and the newest version, because that is
// everything completion shows: the name, its slash if it is a namespace, and
// the version or "no bottle here" that lsNote builds from `Versions`. It
// carries no dependencies, which is why it is 8% of the file.

// nameIndexVersion is the format version in the header. An unknown version is
// refused — and refused means "fall back to the JSON", so a future pkgx
// writing a v2 index cannot be misread by this one.
const nameIndexVersion = 1

// nameIndexMagic opens the header, so a file that is not this is not parsed as
// though it were.
const nameIndexMagic = "#pkgx-names"

// catalogNamesPath is the index beside the catalogue it describes.
func catalogNamesPath(osn, arch string) string {
	return filepath.Join(bottle.Dir(), "catalog", osn+"-"+arch+".names")
}

// writeNameIndex writes the index for a catalogue whose JSON is jsonBytes long.
//
// Sorted, so the file is stable between runs over the same catalogue: a
// derived file that changes when its source did not is a file nobody can
// diff.
func writeNameIndex(path string, cat bottle.Catalog, jsonBytes int64) error {
	names := make([]string, 0, len(cat.Projects))
	vers := make(map[string]string, len(cat.Projects))
	for _, p := range cat.Projects {
		names = append(names, p.Project)
		if len(p.Versions) > 0 {
			vers[p.Project] = p.Versions[0]
		}
	}
	sort.Strings(names)
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %d %d %d\n", nameIndexMagic, nameIndexVersion, jsonBytes, len(names))
	for _, n := range names {
		// A TAB separates them, and a project name cannot contain one: the
		// pantry's names are path segments.
		fmt.Fprintf(&b, "%s\t%s\n", n, vers[n])
	}
	return osWriteFile(path, b.Bytes(), 0o644)
}

// readNameIndex returns the catalogue the index describes, and whether it may
// be used.
//
// The second return is `false` for every doubt — absent, truncated, a version
// this pkgx does not know, a size or count that disagrees with the JSON beside
// it. There is no error to report, because the caller's answer to all of them
// is the same: read the JSON instead.
func readNameIndex(indexPath, jsonPath string) (bottle.Catalog, bool) {
	f, err := os.Open(indexPath)
	if err != nil {
		return bottle.Catalog{}, false
	}
	defer f.Close()
	// ⛔ THE SIZE OF THE JSON, NOT OF THE INDEX. The question is whether this
	// index was made from the catalogue that is here now, and the only cheap
	// evidence is the catalogue's own size.
	st, err := osStat(jsonPath)
	if err != nil {
		return bottle.Catalog{}, false
	}
	return parseNameIndex(f, st.Size())
}

// parseNameIndex is the format, apart from the files.
//
// Separated so the mid-read failure arm can be ENTERED by a test. An I/O error
// that happens after a successful header read cannot be contrived from a real
// file on a real filesystem, and an error arm no test can reach is an error arm
// nobody has checked — this repository's own coverage profile is what pointed
// at it.
func parseNameIndex(r io.Reader, jsonBytes int64) (bottle.Catalog, bool) {
	br := bufio.NewReader(r)
	header, err := br.ReadString('\n')
	if err != nil {
		return bottle.Catalog{}, false
	}
	wantBytes, wantCount, ok := parseNameIndexHeader(header)
	if !ok {
		return bottle.Catalog{}, false
	}
	// The header has to describe the catalogue that is actually here.
	if jsonBytes != wantBytes {
		return bottle.Catalog{}, false
	}

	cat := bottle.Catalog{Projects: make([]bottle.CatalogProject, 0, wantCount)}
	for {
		line, err := br.ReadString('\n')
		if line = strings.TrimRight(line, "\n"); line != "" {
			name, ver, _ := strings.Cut(line, "\t")
			// ⛔ THE SAME VALIDATOR THE CATALOGUE PARSER USES, and this is
			// the whole reason it is called here.
			//
			// Measured 2026-10-11, before this line existed: an index naming
			// `../outside` completed to
			//
			//	../outside	1.0  ✓ 9.9.9
			//
			// and the `✓ 9.9.9` is a directory OUTSIDE $PKGX_DIR, reported as
			// installed — because lsNote asks bottle.InstalledVersions, which
			// does filepath.Join(dir, project). bottle.UnmarshalCatalog
			// refuses that name outright ("has a `..` segment"); this reader
			// built a Catalog by hand and so skipped the refusal.
			//
			// A fast path that reaches the same data must apply the same
			// checks: the guard belongs to the DATA, not to whichever reader
			// got there first.
			if err := bottle.ValidateProjectName(name); err != nil {
				return bottle.Catalog{}, false
			}
			p := bottle.CatalogProject{Project: name}
			if ver != "" {
				p.Versions = []string{ver}
			}
			cat.Projects = append(cat.Projects, p)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return bottle.Catalog{}, false
		}
	}
	// A count that disagrees means a truncated write or a stale pairing, and
	// a SHORT index is the dangerous direction: completion would quietly stop
	// offering the projects that fell off the end.
	if len(cat.Projects) != wantCount {
		return bottle.Catalog{}, false
	}
	return cat, true
}

// parseNameIndexHeader reads `#pkgx-names <ver> <jsonBytes> <projects>`.
func parseNameIndexHeader(line string) (jsonBytes int64, count int, ok bool) {
	fields := strings.Fields(strings.TrimRight(line, "\n"))
	if len(fields) != 4 || fields[0] != nameIndexMagic {
		return 0, 0, false
	}
	v, err := strconv.Atoi(fields[1])
	if err != nil || v != nameIndexVersion {
		return 0, 0, false
	}
	jsonBytes, err = strconv.ParseInt(fields[2], 10, 64)
	if err != nil || jsonBytes <= 0 {
		return 0, 0, false
	}
	count, err = strconv.Atoi(fields[3])
	if err != nil || count < 0 {
		return 0, 0, false
	}
	return jsonBytes, count, true
}

// completionCatalog is the catalogue the COMPLETION path reads, and the only
// caller of the index.
//
// `pkgx ls`, `--tree`, `search` and `browse` keep reading the JSON: they need
// dependencies and summaries, which the index does not carry, and none of them
// runs on a keystroke.
//
// PKGX_CATALOG wins outright. Somebody who named a file is inspecting that
// file, and there is no index for it — answering from one built beside a
// different catalogue would be the exact disagreement this file is careful
// about.
func completionCatalog(dir string) bottle.Catalog {
	if os.Getenv("PKGX_CATALOG") == "" {
		osn, arch := bottle.HostSlug()
		if cat, ok := readNameIndex(catalogNamesPath(osn, arch), catalogPath(osn, arch)); ok {
			return cat
		}
	}
	cat, _, _ := browseCatalog(dir)
	return cat
}
