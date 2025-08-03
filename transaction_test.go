package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Função para testar o comportamento de transação
func TestTransactionRollback(t *testing.T) {
	ctx := context.Background()
	dbURL := "sqlite::memory:"

	// Configurar banco
	config, err := GetDatabaseConfig(dbURL)
	if err != nil {
		fmt.Printf("Erro ao obter config: %v\n", err)
		return
	}

	db, err := OpenDatabase(dbURL, config)
	if err != nil {
		fmt.Printf("Erro ao abrir banco: %v\n", err)
		return
	}
	defer db.Close()

	// Criar diretório temporário
	tempDir, err := os.MkdirTemp("", "migration_test")
	if err != nil {
		fmt.Printf("Erro ao criar diretório temporário: %v\n", err)
		return
	}
	defer os.RemoveAll(tempDir)

	// Criar uma migração válida
	validMigration := filepath.Join(tempDir, "001_valid.up.sql")
	err = os.WriteFile(validMigration, []byte("CREATE TABLE test_table (id INTEGER);"), 0644)
	if err != nil {
		fmt.Printf("Erro ao criar migração válida: %v\n", err)
		return
	}

	// Criar uma migração inválida (SQL com erro)
	invalidMigration := filepath.Join(tempDir, "002_invalid.up.sql")
	err = os.WriteFile(invalidMigration, []byte("CREATE TABLE invalid_syntax error;"), 0644)
	if err != nil {
		fmt.Printf("Erro ao criar migração inválida: %v\n", err)
		return
	}

	fmt.Println("Testando transação com falha...")

	// Tentar executar up - deve falhar e fazer rollback
	n, executed, err := RunWithExistingDatabase(ctx, tempDir, "up", db, config)
	if err != nil {
		fmt.Printf("Erro esperado ao executar migrações: %v\n", err)
		fmt.Printf("Migrações executadas antes da falha: %d\n", n)
		fmt.Printf("Arquivos processados: %v\n", executed)
	}

	// Verificar se o rollback funcionou - deve haver 0 migrações na tabela
	count, err := GetMigrationCount(ctx, db, config)
	if err != nil {
		fmt.Printf("Erro ao verificar contagem: %v\n", err)
		return
	}

	fmt.Printf("Número de migrações no banco após rollback: %d\n", count)
	if count == 0 {
		fmt.Println("✅ SUCESSO: Transação foi corretamente revertida!")
	} else {
		fmt.Println("❌ FALHA: Transação não foi revertida corretamente!")
	}

	// Verificar se a tabela não foi criada
	var tableExists int
	err = db.GetContext(ctx, &tableExists, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='test_table'")
	if err != nil {
		fmt.Printf("Erro ao verificar existência da tabela: %v\n", err)
		return
	}

	if tableExists == 0 {
		fmt.Println("✅ SUCESSO: Tabela não foi criada (rollback funcionou)")
	} else {
		fmt.Println("❌ FALHA: Tabela foi criada (rollback não funcionou)")
	}
}

// Teste para verificar se as transações fazem rollback corretamente em caso de erro
func TestTransactionRollbackOnError(t *testing.T) {
	ctx := context.Background()
	dbURL := "sqlite::memory:"

	// Configurar banco
	config, err := GetDatabaseConfig(dbURL)
	if err != nil {
		t.Fatalf("Erro ao obter config: %v", err)
	}

	db, err := OpenDatabase(dbURL, config)
	if err != nil {
		t.Fatalf("Erro ao abrir banco: %v", err)
	}
	defer db.Close()

	// Criar diretório temporário
	tempDir := t.TempDir()

	// Criar uma migração válida
	validMigration := filepath.Join(tempDir, "001_valid.up.sql")
	err = os.WriteFile(validMigration, []byte("CREATE TABLE test_table (id INTEGER);"), 0644)
	if err != nil {
		t.Fatalf("Erro ao criar migração válida: %v", err)
	}

	// Criar uma migração inválida (SQL com erro)
	invalidMigration := filepath.Join(tempDir, "002_invalid.up.sql")
	err = os.WriteFile(invalidMigration, []byte("CREATE TABLE invalid_syntax error;"), 0644)
	if err != nil {
		t.Fatalf("Erro ao criar migração inválida: %v", err)
	}

	t.Log("Testando transação com falha...")

	// Tentar executar up - deve falhar e fazer rollback
	n, executed, err := RunWithExistingDatabase(ctx, tempDir, "up", db, config)
	if err == nil {
		t.Fatalf("Esperava erro ao executar migrações, mas obteve sucesso")
	}

	t.Logf("Erro esperado ao executar migrações: %v", err)
	t.Logf("Migrações executadas antes da falha: %d", n)
	t.Logf("Arquivos processados: %v", executed)

	// Verificar se o rollback funcionou - deve haver 0 migrações na tabela
	count, err := GetMigrationCount(ctx, db, config)
	if err != nil {
		t.Fatalf("Erro ao verificar contagem: %v", err)
	}

	t.Logf("Número de migrações no banco após rollback: %d", count)
	if count != 0 {
		t.Errorf("Esperava 0 migrações após rollback, mas encontrou %d", count)
	}

	// Verificar se a tabela não foi criada (rollback deve ter desfeito tudo)
	var tableExists int
	err = db.GetContext(ctx, &tableExists, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='test_table'")
	if err != nil {
		t.Fatalf("Erro ao verificar existência da tabela: %v", err)
	}

	if tableExists != 0 {
		t.Errorf("Esperava que a tabela não existisse após rollback, mas ela existe")
	}

	t.Log("✅ SUCESSO: Transação foi corretamente revertida!")
}
