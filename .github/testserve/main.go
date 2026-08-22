// Command testserve serves a directory over HTTP for the CI fixture runs.
//
// It replaces `python -m http.server`, which this repository should not depend
// on -- and which on a Windows runner can resolve to the Microsoft Store
// app-execution alias, a stub that exits without serving anything and without
// failing loudly. Go is already required here; python is not.
//
// It prints READY once the listener is bound, so the caller can wait for the
// port instead of sleeping a guessed number of seconds.
//
//	go run .github/testserve/main.go -dir dist -addr 127.0.0.1:8199
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
)

func main() {
	dir := flag.String("dir", ".", "directory to serve")
	addr := flag.String("addr", "127.0.0.1:8199", "address to listen on")
	flag.Parse()

	// Bind before announcing: READY on stdout means the port is accepting, so a
	// caller that waits for it cannot race the listener.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("testserve: listen %s: %v", *addr, err)
	}
	fmt.Println("READY", ln.Addr())
	os.Stdout.Sync()

	if err := http.Serve(ln, http.FileServer(http.Dir(*dir))); err != nil {
		log.Fatalf("testserve: serve: %v", err)
	}
}
