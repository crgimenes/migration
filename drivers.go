package main

// The SQL drivers live with the binary, not with the library packages:
// an application embedding core/introspect/drift must not inherit the
// PostgreSQL or the (large) SQLite driver it does not use.
//
// PostgreSQL goes through pgx's database/sql adapter — the one PostgreSQL
// package of the house (dbv and keikiban already use pgx); lib/pq is in
// maintenance mode upstream and left the ecosystem.
import (
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)
