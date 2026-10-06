package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// The key loop, separated from the terminal so that a test can drive every
// key without one — and so that the thing CI runs is the thing a person
// runs, rather than a second code path that only looks the same.

// browseLoop puts the terminal in raw mode and runs until q.
func browseLoop(b *browser, f *os.File, stdout, stderr io.Writer) int {
	fd := int(f.Fd())
	old, err := termMakeRaw(fd)
	if err != nil {
		// Not fatal: a terminal that refuses raw mode still reads fine
		// line by line, and refusing to run would be worse than a browser
		// that needs Enter.
		fmt.Fprintln(stderr, "pkgx: this terminal will not go raw; press Enter after each key")
	} else {
		defer func() { _ = termRestore(fd, old) }()
	}
	w, h := 80, 24
	if cw, ch, err := termGetSize(fd); err == nil && ch > 4 {
		w, h = cw, ch
	}
	_ = w

	in := bufio.NewReader(f)
	for {
		b.render(stderr, h)
		k, err := readKey(in)
		if err != nil {
			break
		}
		if quit := applyKey(b, k, in, stderr); quit {
			break
		}
	}
	// The screen is chrome and goes to stderr; the PINS are the result and
	// go to stdout, so `pkgx +$(pkgx browse)` composes. A browser whose
	// decoration reached stdout would put its own borders on a command
	// line.
	fmt.Fprint(stderr, "\x1b[H\x1b[2J")
	for _, p := range b.pinned {
		fmt.Fprintln(stdout, p)
	}
	return 0
}

// key is one keystroke, already decoded out of its escape sequence.
type key string

const (
	keyUp     key = "up"
	keyDown   key = "down"
	keyLeft   key = "left"
	keyRight  key = "right"
	keyTab    key = "tab"
	keyEnter  key = "enter"
	keySpace  key = "space"
	keySlash  key = "/"
	keyQuit   key = "q"
	keyIgnore key = ""
)

// readKey decodes one keystroke, including the three-byte arrow sequences.
//
// An arrow is ESC [ A. Read as single bytes it is an escape, a bracket and
// a letter — and the letter is a perfectly good command, so an up-arrow
// would quit on `A`… or, with this program's bindings, do whatever `A`
// does. Decoding has to happen here or every binding becomes a hazard.
func readKey(r *bufio.Reader) (key, error) {
	c, err := r.ReadByte()
	if err != nil {
		return keyIgnore, err
	}
	switch c {
	case 0x1b:
		// ESC alone (the user pressed Escape) must not block waiting for a
		// second byte that is not coming. Buffered() tells us whether the
		// rest of a sequence has already arrived, which it has when the
		// terminal sent one.
		if r.Buffered() < 2 {
			return keyIgnore, nil
		}
		if b, _ := r.ReadByte(); b != '[' {
			return keyIgnore, nil
		}
		d, _ := r.ReadByte()
		switch d {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C':
			return keyRight, nil
		case 'D':
			return keyLeft, nil
		}
		return keyIgnore, nil
	case '\t':
		return keyTab, nil
	case '\r', '\n':
		return keyEnter, nil
	case ' ':
		return keySpace, nil
	case '/':
		return keySlash, nil
	case 'q', 3, 4: // q, ^C, ^D
		return keyQuit, nil
	case 'k':
		return keyUp, nil
	case 'j':
		return keyDown, nil
	case 'h':
		return keyLeft, nil
	case 'l':
		return keyRight, nil
	}
	return keyIgnore, nil
}

// applyKey moves the browser and reports whether to stop.
func applyKey(b *browser, k key, in *bufio.Reader, stderr io.Writer) bool {
	rows := b.rows()
	switch k {
	case keyQuit:
		return true
	case keyUp:
		if b.sel > 0 {
			b.sel--
		}
	case keyDown:
		if b.sel < len(rows)-1 {
			b.sel++
		}
	case keyRight, keyEnter:
		b.enter()
	case keyLeft:
		b.up()
	case keyTab:
		b.deps = !b.deps
		b.sel = 0
	case keySpace:
		if len(rows) > 0 {
			b.pin(rows[b.sel])
		}
	case keySlash:
		b.filter, b.sel = readLine(in, stderr), 0
		if b.filter == "" {
			b.message = "search cleared"
		}
	}
	return false
}

// readLine reads a search term with the terminal still raw, echoing as it
// goes — the shell's line editor is not available in raw mode, and a
// prompt that shows nothing while you type is a prompt people abandon.
func readLine(r *bufio.Reader, w io.Writer) string {
	var sb strings.Builder
	fmt.Fprint(w, "\r\n/")
	for {
		c, err := r.ReadByte()
		if err != nil {
			return sb.String()
		}
		switch {
		case c == '\r' || c == '\n':
			return sb.String()
		case c == 0x1b:
			return "" // Escape abandons the search
		case c == 127 || c == 8:
			s := sb.String()
			if s != "" {
				sb.Reset()
				sb.WriteString(s[:len(s)-1])
				fmt.Fprint(w, "\b \b")
			}
		case c >= 0x20 && c < 0x7f:
			sb.WriteByte(c)
			fmt.Fprintf(w, "%c", c)
		}
	}
}
