# Migration Tool

[![MIT Licensed](https://img.shields.io/badge/license-MIT-green.svg)](https://tldrlegal.com/license/mit-license)
[![Go Version](https://img.shields.io/badge/go-1.24+-blue.svg)](https://golang.org)

Um utilitário de migração PostgreSQL simples e eficiente com suporte a transações, construído usando apenas bibliotecas padrão do Go.

## Características

- ✅ **Bibliotecas Padrão**: Usa apenas `flag` do Go, sem dependências CLI externas
- ✅ **Transações**: Cada migração é executada em uma transação segura
- ✅ **PostgreSQL**: Suporte nativo ao PostgreSQL
- ✅ **Variáveis de Ambiente**: Configuração flexível via env vars ou flags
- ✅ **Controle de Versão**: Acompanha migrações executadas
- ✅ **Rollback**: Suporte para reverter migrações

## Instalação

```bash
go install github.com/crgimenes/migration@latest
```

Ou compile do código fonte:

```bash
git clone https://github.com/crgimenes/migration.git
cd migration
go build -o migration
```

## Uso

### Configuração via Variáveis de Ambiente

```bash
export DATABASE_URL="postgres://user:password@localhost:5432/dbname?sslmode=disable"
export MIGRATIONS="./migrations"
export ACTION="status"
./migration
```

### Configuração via Flags

#### Verificar Status das Migrações

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "status"
```

#### Executar Todas as Migrações Pendentes

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "up"
```

#### Executar um Número Específico de Migrações

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "up 2"
```

#### Reverter Todas as Migrações

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "down"
```

#### Reverter um Número Específico de Migrações

```bash
./migration -url "postgres://user:password@localhost:5432/dbname?sslmode=disable" -dir "./migrations" -action "down 1"
```

### Ajuda e Versão

```bash
./migration -help
./migration -version
```

## Estrutura dos Arquivos de Migração

Os arquivos de migração devem seguir a convenção de nomenclatura:

```
001_create_users_table.up.sql
001_create_users_table.down.sql
002_add_email_index.up.sql
002_add_email_index.down.sql
```

### Exemplo de Migração

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

## Opções de Configuração

| Flag | Variável de Ambiente | Descrição |
|------|---------------------|-----------|
| `-url` | `DATABASE_URL` | URL de conexão do PostgreSQL |
| `-dir` | `MIGRATIONS` | Diretório contendo os arquivos de migração |
| `-action` | `ACTION` | Ação a ser executada (`up`, `down`, `status`) |
| `-help` | - | Mostra ajuda |
| `-version` | - | Mostra versão |

## Dependências

Este projeto usa apenas dependências mínimas:

- `github.com/jmoiron/sqlx` - Extensões SQL para Go
- `github.com/lib/pq` - Driver PostgreSQL puro Go
- `golang.org/x/xerrors` - Tratamento de erros

## Exemplo Completo

```bash
# 1. Configurar variáveis de ambiente
export DATABASE_URL="postgres://postgres:password@localhost:5432/myapp?sslmode=disable"
export MIGRATIONS="./migrations"

# 2. Verificar status
./migration -action "status"

# 3. Executar migrações
./migration -action "up"

# 4. Se necessário, reverter
./migration -action "down 1"
```

## Contribuição

Contribuições são bem-vindas! Por favor, abra uma issue ou envie um pull request.

## Licença

Este projeto está licenciado sob a Licença MIT - veja o arquivo [LICENSE](LICENSE) para detalhes.
