package main

import (
	"github.com/felipeweb/migration/cmd"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

func main() {
	if err := cmd.Execute(); err != nil {
		logrus.Fatal(err)
	}
}
