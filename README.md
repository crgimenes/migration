# Migration Tool

[![MIT Licensed](https://img.shields.io/badge/license-MIT-green.svg)](https://tldrlegal.com/license/mit-license)
[![Go Version](https://img.shields.io/badge/go-1.24+-blue.svg)](https://golang.org)

A simple and efficient database migration utility with transaction support for PostgreSQL and SQLite, built using only Go standard libraries.

## Features

- **Transactions**: Each migration runs in a safe transaction
- **Multi-Database**: Supports PostgreSQL and SQLite with automatic detection
- **Environment Variables**: Flexible configuration via env vars or flags
- **Version Control**: Tracks executed migrations
- **Rollback**: Support for reverting migrations

## ⚠️ Important Notice

**This tool automatically creates and manages a `schema_migrations` table in your database.**

- The table stores migration version numbers to track which migrations have been executed
- **Table Structure**: `schema_migrations (version INTEGER PRIMARY KEY)`
- **Auto-Creation**: If the table doesn't exist, it will be created automatically on first run
- **No Conflicts**: The table name is standard and shouldn't conflict with your application tables
- **Manual Management**: You can query this table to see migration status: `SELECT * FROM schema_migrations ORDER BY version;`

**What this means for you:**

- **Safe**: The tool only manages its own tracking table
- **Automatic**: No manual setup required
- **Standard**: Uses common migration table naming convention
- **Awareness**: Be aware this table will exist in your database schema

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
# PostgreSQL
./migration \
  -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" \
  -dir "./migrations" \
  -action "status"

# SQLite file
./migration \
  -url "sqlite:///path/to/database.db" \
  -dir "./migrations" \
  -action "status"

# SQLite in-memory (for testing)
./migration \
  -url "sqlite::memory:" \
  -dir "./migrations" \
  -action "status"
```

#### Run All Pending Migrations

```bash
# PostgreSQL
./migration \
  -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" \
  -dir "./migrations" \
  -action "up"

# SQLite
./migration \
  -url "sqlite:///path/to/database.db" \
  -dir "./migrations" \
  -action "up"
```

#### Run Specific Number of Migrations

```bash
./migration \
  -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" \
  -dir "./migrations" \
  -action "up 2"
```

#### Revert All Migrations

```bash
./migration \
  -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" \
  -dir "./migrations" \
  -action "down"
```

#### Revert Specific Number of Migrations

```bash
./migration \
  -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" \
  -dir "./migrations" \
  -action "down 1"
```

### Help and Version

```bash
./migration -help
./migration -version
```

## Migration File Structure

Migration files must follow the naming convention:

```text
001_create_users_table.up.sql
001_create_users_table.down.sql
002_add_email_index.up.sql
002_add_email_index.down.sql
```

### Migration Example

**001_create_users_table.up.sql:**

```sql
-- PostgreSQL version
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- SQLite version (if using SQLite)
-- CREATE TABLE users (
--     id INTEGER PRIMARY KEY AUTOINCREMENT,
--     name TEXT NOT NULL,
--     email TEXT UNIQUE NOT NULL,
--     created_at DATETIME DEFAULT CURRENT_TIMESTAMP
-- );
```

**001_create_users_table.down.sql:**

```sql
DROP TABLE IF EXISTS users;
```

### Advanced Migration Examples

**002_add_user_profile.up.sql:**

```sql
-- Add profile fields to users table
ALTER TABLE users
ADD COLUMN avatar_url TEXT,
ADD COLUMN bio TEXT,
ADD COLUMN is_active BOOLEAN DEFAULT true;

-- Create user sessions table
CREATE TABLE user_sessions (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    session_token VARCHAR(255) NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_user_sessions_token ON user_sessions(session_token);
CREATE INDEX idx_user_sessions_user_id ON user_sessions(user_id);
```

**002_add_user_profile.down.sql:**

```sql
-- Remove in reverse order
DROP INDEX IF EXISTS idx_user_sessions_user_id;
DROP INDEX IF EXISTS idx_user_sessions_token;
DROP TABLE IF EXISTS user_sessions;

-- Remove columns (PostgreSQL syntax)
ALTER TABLE users
DROP COLUMN IF EXISTS avatar_url,
DROP COLUMN IF EXISTS bio,
DROP COLUMN IF EXISTS is_active;
```

### Database-Specific Migration Tips

#### PostgreSQL Features

```sql
-- Use transactions (automatically handled by migration tool)
-- Use IF EXISTS/IF NOT EXISTS for safety
-- Consider using SERIAL for auto-increment IDs
-- Use proper data types: VARCHAR, TEXT, TIMESTAMP, etc.
```

#### SQLite Considerations

```sql
-- Use INTEGER PRIMARY KEY for auto-increment
-- Use TEXT instead of VARCHAR
-- Use DATETIME instead of TIMESTAMP
-- Be careful with ALTER TABLE limitations
-- Some operations require table recreation
```

### Troubleshooting

#### Common Issues

**Migration not found:**

```bash
# Check if files exist and have correct naming
ls -la migrations/
```

**Database connection issues:**

```bash
# Test connection manually
psql $DATABASE_URL -c "SELECT 1;"
# Or for SQLite
sqlite3 /path/to/database.db ".tables"
```

**Permission errors:**

```bash
# Make sure database user has proper permissions
# For PostgreSQL: GRANT CREATE, ALTER, DROP ON DATABASE
# For SQLite: Check file permissions
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Running Tests

```bash
# Run all tests
go test -v

# Run with coverage
go test -v -cover

# Run specific test
go test -v -run TestSpecificFunction
```

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
