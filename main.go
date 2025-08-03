package main

import (
	"log"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func main() {
	if err := Execute(); err != nil {
		log.Fatal(err)
	}
}
