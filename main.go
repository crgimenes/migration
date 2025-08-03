package main

import (
	"log"

	_ "github.com/lib/pq"
)

func main() {
	if err := Execute(); err != nil {
		log.Fatal(err)
	}
}
