# migration
[![MIT Licensed](https://img.shields.io/badge/license-MIT-green.svg)](https://tldrlegal.com/license/mit-license)

PostgreSQL migration tool with transactions

```console
./migration exec -url "postgres://postgres@localhost:5432/dbname?sslmode=disable" -dir ./fixtures -action up
```

```console
./migration exec -url "postgres://postgres@localhost:5432/dbname?sslmode=disable" -dir ./fixtures -action down 2
```

```console
./migration exec -url "postgres://postgres@localhost:5432/dbname?sslmode=disable" -dir ./fixtures -action status
```
