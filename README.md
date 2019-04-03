# migration
[![Build Status](https://travis-ci.org/felipeweb/migration.svg?branch=master)](https://travis-ci.org/felipeweb/migration)
[![Go Report Card](https://goreportcard.com/badge/github.com/felipeweb/migration)](https://goreportcard.com/report/github.com/felipeweb/migration)
[![GoDoc](https://godoc.org/github.com/felipeweb/migration?status.png)](https://godoc.org/github.com/felipeweb/migration)
[![Go project version](https://badge.fury.io/go/github.com%2Ffelipeweb%2Fmigration.svg)](https://badge.fury.io/go/github.com/felipeweb/migration)
[![MIT Licensed](https://img.shields.io/badge/license-MIT-green.svg)](https://tldrlegal.com/license/mit-license)

SQL migration tool

```console
go run cmd/migration/main.go \
    -url="postgres://postgres@localhost:5432/dbname?sslmode=disable" \
    -dir=./fixtures \
    -action=up
```

```console
go run cmd/migration/main.go exec -url="postgres://postgres@localhost:5432/dbname?sslmode=disable" -dir=./fixtures -action=down