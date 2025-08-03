package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"golang.org/x/xerrors"
)

// upFiles search for migration up files and return
// a sorted array with the path of all found files
func upFiles(dir string) (files []string, err error) {
	files, err = filepath.Glob(filepath.Join(dir, "*.up.sql"))
	return
}

// downFiles search for migration down files and return
// a sorted array with the path of all found files
func downFiles(dir string, n int) (files []string, err error) {
	files, err = filepath.Glob(filepath.Join(dir, "*.down.sql"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	files = files[len(files)-n:]
	return
}

func up(ctx context.Context, source string, start, n int, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	files, err := upFiles(source)
	if err != nil {
		return
	}
	number, executed, err = execUp(ctx, files, start, n, db, config)
	return
}

func down(ctx context.Context, source string, start, n int, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	nfiles, err := GetMigrationMax(ctx, db, config)
	if err != nil {
		return
	}
	if n == 0 {
		n = nfiles
	}
	files, err := downFiles(source, nfiles)
	if err != nil {
		return
	}
	number, executed, err = execDown(ctx, files, start, n, db, config)
	return
}

func execDown(ctx context.Context, files []string, start, n int, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	i := len(files)
	if i == 0 {
		return
	}
	for k, f := range files[start:n] {
		var b []byte
		b, err = os.ReadFile(f) // nolint
		if err != nil {
			return
		}
		var tx *sqlx.Tx
		tx, err = db.Beginx()
		if err != nil {
			return
		}
		_, err = tx.ExecContext(ctx, string(b))
		if err != nil {
			tx.Rollback() // nolint
			return
		}
		err = DeleteMigration(ctx, i, tx, config)
		if err != nil {
			tx.Rollback() // nolint
			return
		}
		err = tx.Commit()
		if err != nil {
			return
		}
		i--
		number = k + 1
		executed = append(executed, f)
	}
	return
}

func execUp(ctx context.Context, files []string, start, n int, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	if n == 0 {
		n = len(files)
	}
	i := start + 1
	for k, f := range files[start:n] {
		var b []byte
		b, err = os.ReadFile(f) // nolint
		if err != nil {
			return
		}
		var tx *sqlx.Tx
		tx, err = db.Beginx()
		if err != nil {
			return
		}
		_, err = tx.ExecContext(ctx, string(b))
		if err != nil {
			tx.Rollback() // nolint
			return
		}
		err = InsertMigration(ctx, i, tx, config)
		if err != nil {
			tx.Rollback() // nolint
			return
		}
		err = tx.Commit()
		if err != nil {
			return
		}
		i++
		number = k + 1
		executed = append(executed, f)
	}
	return
}

func parsePar(m []string) (n int, err error) {
	if len(m) > 1 {
		n, err = strconv.Atoi(m[1])
		if err != nil {
			err = xerrors.Errorf("invalid syntax")
			return
		}
	}
	return
}

// Run parses and performs the required migration
func Run(ctx context.Context, source, url, migrate string) (n int, executed []string, err error) {
	db, config, err := OpenDatabase(ctx, url)
	if err != nil {
		return
	}
	defer db.Close()

	return RunWithDatabase(ctx, source, migrate, db, config)
}

// RunWithDatabase runs migration with an existing database connection
func RunWithDatabase(ctx context.Context, source, migrate string, db *sqlx.DB, config *DatabaseConfig) (n int, executed []string, err error) {
	m := strings.Split(migrate, " ")
	if len(m) > 2 {
		err = xerrors.New("the number of migration parameters is incorrect")
		return
	}
	info, err := os.Stat(source)
	if err != nil {
		return
	}
	if !info.IsDir() {
		err = xerrors.Errorf("%v is not a directory", source)
		return
	}
	err = InitSchemaMigrations(ctx, db, config)
	if err != nil {
		return
	}
	switch m[0] {
	case "up":
		n, executed, err = doUp(ctx, m, source, db, config)
	case "down":
		n, executed, err = doDown(ctx, m, source, db, config)
	case "status":
		n, executed, err = Status(ctx, source, db, config)
	default:
		err = xerrors.Errorf("unknown migration command")
	}
	return
}

// Status checks database status
func Status(ctx context.Context, source string, db *sqlx.DB, config *DatabaseConfig) (int, []string, error) {
	n, err := GetMigrationMax(ctx, db, config)
	if err != nil {
		return 0, nil, err
	}
	up, err := upFiles(source)
	if err != nil {
		return 0, nil, err
	}
	diff := len(up) - n
	if diff == 0 {
		return 0, nil, nil
	}
	if diff < 0 {
		diff = -1 * diff
	}
	return diff, up[len(up)-diff:], nil
}

func doDown(ctx context.Context, m []string, source string, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	n, err := parsePar(m)
	if err != nil {
		return
	}
	number, executed, err = down(ctx, source, 0, n, db, config)
	return
}

func doUp(ctx context.Context, m []string, source string, db *sqlx.DB, config *DatabaseConfig) (number int, executed []string, err error) {
	n, err := parsePar(m)
	if err != nil {
		return
	}
	start, err := GetMigrationMax(ctx, db, config)
	if err != nil {
		return
	}
	number, executed, err = up(ctx, source, start, n, db, config)
	return
}
