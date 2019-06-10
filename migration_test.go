package migration

import (
	"context"
	"reflect"
	"testing"

	// pq driver for tests
	_ "github.com/lib/pq"
)

func Test_upFiles(t *testing.T) {
	tests := []struct {
		name      string
		wantFiles []string
		wantErr   bool
		path      string
	}{
		{
			name: "list files",
			path: "testdata",
			wantFiles: []string{
				"testdata/001_name.up.sql",
				"testdata/002_b_name.up.sql",
				"testdata/003_a_name.up.sql",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFiles, err := upFiles(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("upFiles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotFiles, tt.wantFiles) {
				t.Errorf("upFiles() = %v, want %v", gotFiles, tt.wantFiles)
			}
		})
	}
}

func Test_downFiles(t *testing.T) {
	tests := []struct {
		name      string
		wantFiles []string
		wantErr   bool
		path      string
	}{
		{
			name: "list files",
			path: "testdata",
			wantFiles: []string{
				"testdata/003_a_name.down.sql",
				"testdata/002_b_name.down.sql",
				"testdata/001_name.down.sql",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFiles, err := downFiles(tt.path, 3)
			if (err != nil) != tt.wantErr {
				t.Errorf("downFiles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotFiles, tt.wantFiles) {
				t.Errorf("downFiles() = %v, want %v", gotFiles, tt.wantFiles)
			}
		})
	}
}

func TestRun(t *testing.T) {
	url := "postgres://postgres@localhost:5432/test?sslmode=disable"
	source := "./testdata"
	_, _, err := Run(context.Background(), "./test", url, "up")
	if err == nil {
		t.Fatal("err dir is nil")
	}
	_, _, err = Run(context.Background(), "./migration.go", url, "up")
	if err == nil {
		t.Fatal("err dir is nil")
	}
	n, exec, err := Run(context.Background(), source, url, "up")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected n %v but got %v", 3, n)
	}
	if len(exec) != 3 {
		t.Errorf("expected len(exec) %v but got %v", 3, len(exec))
	}
	n, exec, err = Run(context.Background(), source, url, "status")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected n %v but got %v", 0, n)
	}
	if len(exec) != 0 {
		t.Errorf("expected len(exec) %v but got %v", 0, len(exec))
	}
	n, exec, err = Run(context.Background(), source, url, "down")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected n %v but got %v", 3, n)
	}
	if len(exec) != 3 {
		t.Errorf("expected len(exec) %v but got %v", 3, len(exec))
	}
	n, exec, err = Run(context.Background(), source, url, "status")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected n %v but got %v", 3, n)
	}
	if len(exec) != 3 {
		t.Errorf("expected len(exec) %v but got %v", 3, len(exec))
	}
	_, _, err = Run(context.Background(), source, url, "s")
	if err == nil {
		t.Error("err is nil")
	}
}
