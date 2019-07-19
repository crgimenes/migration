package main

import (
	"github.com/gosidekick/migration/v2/cmd"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
	_ "gocloud.dev/postgres/awspostgres"
	_ "gocloud.dev/postgres/gcppostgres"
)

func main() {
	if err := cmd.Execute(); err != nil {
		logrus.Fatal(err)
	}
}
