package agenttools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAndReadTextFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := service.Execute(context.Background(), "list_directory", `{"path":"."}`)
	if !strings.Contains(listed, "README.md") {
		t.Fatalf("unexpected listing: %s", listed)
	}
	read := service.Execute(context.Background(), "read_text_file", `{"path":"README.md"}`)
	if !strings.Contains(read, `"content":"hello"`) {
		t.Fatalf("unexpected read: %s", read)
	}
}

func TestSensitiveAndOutsidePathsAreRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	service, err := New(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"path":".env"}`, `{"path":"escape.txt"}`, `{"path":"` + outsideFile + `"}`} {
		result := service.Execute(context.Background(), "read_text_file", args)
		if !strings.Contains(result, `"error"`) {
			t.Fatalf("expected policy error for %s: %s", args, result)
		}
	}
}

func TestExplicitAllowedPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(outside, "allowed.txt")
	if err := os.WriteFile(file, []byte("allowed"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(root, []string{outside}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := service.Execute(context.Background(), "read_text_file", `{"path":"`+file+`"}`)
	if !strings.Contains(result, `"content":"allowed"`) {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestReadLimitsAndBinaryRejection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary"), []byte{'a', 0, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result := service.Execute(context.Background(), "read_text_file", `{"path":"binary"}`); !strings.Contains(result, "binary") {
		t.Fatalf("expected binary rejection: %s", result)
	}
	for i := 0; i < 5; i++ {
		name := filepath.Join(root, "chunk"+string(rune('a'+i)))
		if err := os.WriteFile(name, []byte(strings.Repeat("x", MaxReadBytes)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		args := `{"path":"chunk` + string(rune('a'+i)) + `"}`
		if result := service.Execute(context.Background(), "read_text_file", args); strings.Contains(result, `"error"`) {
			t.Fatalf("unexpected budget failure: %s", result)
		}
	}
	if result := service.Execute(context.Background(), "read_text_file", `{"path":"chunke"}`); !strings.Contains(result, "budget") {
		t.Fatalf("expected budget exhaustion: %s", result)
	}
}
