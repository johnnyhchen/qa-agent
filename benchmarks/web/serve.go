// Package main serves the benchmark web app's static files.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	port := flag.Int("port", 0, "listen port (0 = random)")
	dir := flag.String("dir", "app", "directory to serve")
	flag.Parse()

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("benchmark web server starting on %s (serving %s)", addr, *dir)
	log.Fatal(http.ListenAndServe(addr, http.FileServer(http.Dir(*dir))))
}
