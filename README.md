# migration
[![Build Status](https://travis-ci.com/felipeweb/migration.svg?branch=master)](https://travis-ci.com/felipeweb/migration)
[![Go Report Card](https://goreportcard.com/badge/github.com/felipeweb/migration)](https://goreportcard.com/report/github.com/felipeweb/migration)
[![GoDoc](https://godoc.org/github.com/felipeweb/migration?status.png)](https://godoc.org/github.com/felipeweb/migration)
[![MIT Licensed](https://img.shields.io/badge/license-MIT-green.svg)](https://tldrlegal.com/license/mit-license)
[![codecov](https://codecov.io/gh/felipeweb/migration/branch/master/graph/badge.svg)](https://codecov.io/gh/felipeweb/migration)

PostgreSQL migration tool with transactions

```console
./migration -url "postgres://postgres@localhost:5432/dbname?sslmode=disable" -dir ./fixtures -action up
```

```console
./migration exec -url "postgres://postgres@localhost:5432/dbname?sslmode=disable" -dir ./fixtures -action down 2
```

```console
./migration exec -url "postgres://postgres@localhost:5432/dbname?sslmode=disable" -dir ./fixtures -action status
```