package main

// The SQL drivers live with the binary, not with the library packages:
// an application embedding core/introspect/drift must not inherit
// lib/pq or the (large) SQLite driver it does not use.
import (
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)
