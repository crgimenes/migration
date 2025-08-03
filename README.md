# Migration Tool

[![MIT Licensed](https://img.shields.io/badge/license-MIT-green.svg)](https://tldrlegal.com/license/mit-license)
[![Go Version](https://img.shields.io/badge/go-1.24+-blue.svg)](https://golang.org)

A simple and efficient PostgreSQL migration utility with transaction support, built using only Go standard libraries.

## Features

- **Standard Libraries**: Uses only Go's `flag` package, no external CLI dependencies
- **Transactions**: Each migration runs in a safe transaction
- **PostgreSQL**: Native PostgreSQL support
- **Environment Variables**: Flexible configuration via env vars or flags
- **Version Control**: Tracks executed migrations
- **Rollback**: Support for reverting migrations

## Installation

```bash
go install github.com/crgimenes/migration@latest
```

Or compile from source:

```bash
git clone https://github.com/crgimenes/migration.git
cd migration
go build -o migration
```

## Usage

### Configuration via Environment Variables

```bash
export DATABASE_URL="postgres://user:password@localhost:5432/dbname?sslmode=disable"
export MIGRATIONS="./migrations"
export ACTION="status"
./migration
```

### Configuration via Flags

#### Check Migration Status

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "status"
```

#### Run All Pending Migrations

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "up"
```

#### Run Specific Number of Migrations

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "up 2"
```

#### Revert All Migrations

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "down"
```

#### Revert Specific Number of Migrations

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "down 1"
```

### Help and Version

```bash
./migration -help
./migration -version
```

## Migration File Structure

Migration files must follow the naming convention:

```
001_create_users_table.up.sql
001_create_users_table.down.sql
002_add_email_index.up.sql
002_add_email_index.down.sql
```

### Migration Example

**001_create_users_table.up.sql:**

```sql
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);
```

**001_create_users_table.down.sql:**

```sql
DROP TABLE users;
```

## Configuration Options

| Flag | Environment Variable | Description |
|------|---------------------|-------------|
| `-url` | `DATABASE_URL` | PostgreSQL connection URL |
| `-dir` | `MIGRATIONS` | Directory containing migration files |
| `-action` | `ACTION` | Action to execute (`up`, `down`, `status`) |
| `-help` | - | Show help |
| `-version` | - | Show version |

## Dependencies

This project uses only minimal dependencies:

- `github.com/jmoiron/sqlx` - SQL extensions for Go
- `github.com/lib/pq` - Pure Go PostgreSQL driver
- `golang.org/x/xerrors` - Error handling

## Complete Example

```bash
# 1. Set environment variables
export DATABASE_URL="postgres://postgres:password@localhost:5432/myapp?sslmode=disable"
export MIGRATIONS="./migrations"

# 2. Check status
./migration -action "status"

# 3. Run migrations
./migration -action "up"

# 4. If needed, revert
./migration -action "down 1"
```

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
