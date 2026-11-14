// Command api serves the greeting API of the demo.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "the address to listen on")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("api", version)
		return
	}
	srv := &http.Server{Addr: *addr, Handler: newMux(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("api %s listening on %s", version, *addr)
	log.Fatal(srv.ListenAndServe())
}
