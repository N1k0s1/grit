// Command gritd is the grit server.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/n1k0s1/grit/internal/demo"
	"github.com/n1k0s1/grit/internal/server"
	"github.com/n1k0s1/grit/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	db := flag.String("db", "grit.db", "SQLite database path")
	seed := flag.Bool("demo", false, "seed generated example builders")
	flag.Parse()

	st, err := store.Open(*db)
	if err != nil {
		log.Fatal(err)
	}
	if *seed {
		if err := demo.Seed(st); err != nil {
			log.Fatal("seed: ", err)
		}
	}
	key := os.Getenv("GRIT_REVIEWER_KEY")
	if key == "" {
		log.Print("GRIT_REVIEWER_KEY not set: demo mode, review pages are public")
	}
	srv, err := server.New(st, key)
	if err != nil {
		log.Fatal(err)
	}
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("gritd listening on %s", *addr)
	log.Fatal(hs.ListenAndServe())
}
