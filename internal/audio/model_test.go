package audio

import (
	"os"
	"runtime"
	"testing"
)

func TestEmbeddedModelBytes(t *testing.T) {
	bytes := ModelBytes()
	if len(bytes) == 0 {
		t.Fatal("embedded model bytes are empty")
	}
	if len(bytes) < 100_000 {
		t.Fatalf("embedded model size suspiciously small: %d bytes", len(bytes))
	}
}

func TestOpenEmbeddedModel(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("memfd_create is Linux-only")
	}
	handle, err := OpenEmbeddedModel()
	if err != nil {
		t.Fatalf("OpenEmbeddedModel failed: %v", err)
	}
	defer handle.Close()

	path := handle.Path()
	if path == "" {
		t.Fatal("empty virtual path returned")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read from %s: %v", path, err)
	}
	if len(data) != len(stdModelBytes) {
		t.Fatalf("data read from %s has length %d, want %d", path, len(data), len(stdModelBytes))
	}
}
