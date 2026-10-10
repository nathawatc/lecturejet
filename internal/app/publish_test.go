package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/nathawat/lecturejet/internal/config"
)

func TestPreflightExistingDestination(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.mp4")
	if err := os.WriteFile(inputPath, []byte("input video data"), 0o600); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(dir, "output.mp4")

	t.Run("destination absent + force=false -> valid", func(t *testing.T) {
		cfg := config.Default()
		cfg.Input = inputPath
		cfg.Output = outputPath
		cfg.Force = false
		if err := Preflight(cfg); err != nil {
			t.Fatalf("unexpected preflight error: %v", err)
		}
	})

	// Create existing destination
	originalContent := []byte("original output data")
	if err := os.WriteFile(outputPath, originalContent, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("destination present + force=false -> error", func(t *testing.T) {
		cfg := config.Default()
		cfg.Input = inputPath
		cfg.Output = outputPath
		cfg.Force = false
		err := Preflight(cfg)
		if err == nil {
			t.Fatal("expected preflight error for existing destination without --force, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("unexpected error message: %v", err)
		}
		// Verify destination file content remains unchanged
		content, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != string(originalContent) {
			t.Fatalf("destination modified during preflight: got %q, want %q", string(content), string(originalContent))
		}
	})

	t.Run("destination present + force=true -> valid", func(t *testing.T) {
		cfg := config.Default()
		cfg.Input = inputPath
		cfg.Output = outputPath
		cfg.Force = true
		if err := Preflight(cfg); err != nil {
			t.Fatalf("expected preflight with force=true to pass, got: %v", err)
		}
		// Verify destination file content is preserved and not removed or truncated
		content, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != string(originalContent) {
			t.Fatalf("destination modified during preflight with force=true: got %q, want %q", string(content), string(originalContent))
		}
	})
}

func TestPreflightSamePath(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.mp4")
	if err := os.WriteFile(inputPath, []byte("input video data"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("input == output without force -> error", func(t *testing.T) {
		cfg := config.Default()
		cfg.Input = inputPath
		cfg.Output = inputPath
		cfg.Force = false
		if err := Preflight(cfg); err == nil {
			t.Fatal("expected error when input == output without force, got nil")
		}
	})

	t.Run("input == output with force -> success", func(t *testing.T) {
		cfg := config.Default()
		cfg.Input = inputPath
		cfg.Output = inputPath
		cfg.Force = true
		if err := Preflight(cfg); err != nil {
			t.Fatalf("expected nil when input == output with force, got: %v", err)
		}
	})

	t.Run("input vs ./input without force -> error", func(t *testing.T) {
		// Change working directory relative path test
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(cwd) }()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}

		cfg := config.Default()
		cfg.Input = "input.mp4"
		cfg.Output = "./input.mp4"
		cfg.Force = false
		if err := Preflight(cfg); err == nil {
			t.Fatal("expected error when input vs ./input without force, got nil")
		}
	})

	t.Run("input symlink == output existing file without force -> error", func(t *testing.T) {
		symlinkPath := filepath.Join(dir, "symlink.mp4")
		if err := os.Symlink(inputPath, symlinkPath); err != nil {
			t.Skip("symlink not supported on this filesystem")
		}
		defer os.Remove(symlinkPath)

		cfg := config.Default()
		cfg.Input = symlinkPath
		cfg.Output = inputPath
		cfg.Force = false
		if err := Preflight(cfg); err == nil {
			t.Fatal("expected error when input symlink references output file, got nil")
		}

		// Also check inverse: input is real file, output is symlink to it
		cfg.Input = inputPath
		cfg.Output = symlinkPath
		cfg.Force = false
		if err := Preflight(cfg); err == nil {
			t.Fatal("expected error when output symlink references input file, got nil")
		}
	})

	t.Run("input hardlink == output existing file without force -> error", func(t *testing.T) {
		hardlinkPath := filepath.Join(dir, "hardlink.mp4")
		if err := os.Link(inputPath, hardlinkPath); err != nil {
			t.Skip("hardlink not supported on this filesystem")
		}
		defer os.Remove(hardlinkPath)

		cfg := config.Default()
		cfg.Input = hardlinkPath
		cfg.Output = inputPath
		cfg.Force = false
		if err := Preflight(cfg); err == nil {
			t.Fatal("expected error when input hardlink references same file as output, got nil")
		}
	})
}

func TestPreflightRejectsDirectoryDestination(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.mp4")
	if err := os.WriteFile(inputPath, []byte("input video data"), 0o600); err != nil {
		t.Fatal(err)
	}

	outSubdir := filepath.Join(dir, "outdir.mp4")
	if err := os.Mkdir(outSubdir, 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Input = inputPath
	cfg.Output = outSubdir
	cfg.Force = true
	err := Preflight(cfg)
	if err == nil {
		t.Fatal("expected error when output path is a directory, got nil")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestPublish(t *testing.T) {
	t.Run("force=false + destination absent -> publish succeeds", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "temp.mp4")
		dst := filepath.Join(dir, "final.mp4")
		if err := os.WriteFile(src, []byte("rendered video content"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := Publish(src, dst, false); err != nil {
			t.Fatalf("expected publish to succeed, got: %v", err)
		}

		if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected source to be moved, but source still exists")
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "rendered video content" {
			t.Fatalf("unexpected destination content: %q", string(data))
		}
	})

	t.Run("force=false + destination exists -> error, existing contents unchanged", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "temp.mp4")
		dst := filepath.Join(dir, "final.mp4")
		if err := os.WriteFile(src, []byte("new rendered content"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("original pre-existing content"), 0o600); err != nil {
			t.Fatal(err)
		}

		err := Publish(src, dst, false)
		if err == nil {
			t.Fatal("expected publish error when destination exists and force=false, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("unexpected error message: %v", err)
		}

		// Verify existing destination contents are unchanged
		dstData, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(dstData) != "original pre-existing content" {
			t.Fatalf("destination content was modified: got %q", string(dstData))
		}

		// Verify source file was NOT removed
		srcData, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if string(srcData) != "new rendered content" {
			t.Fatalf("source content was modified: got %q", string(srcData))
		}
	})

	t.Run("force=true + destination exists -> publish succeeds, destination becomes new contents", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "temp.mp4")
		dst := filepath.Join(dir, "final.mp4")
		if err := os.WriteFile(src, []byte("new rendered content"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("original pre-existing content"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := Publish(src, dst, true); err != nil {
			t.Fatalf("expected publish with force=true to succeed, got: %v", err)
		}

		if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected source to be moved, but source still exists")
		}
		dstData, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(dstData) != "new rendered content" {
			t.Fatalf("destination was not updated: got %q", string(dstData))
		}
	})

	t.Run("regression: destination appearing after preflight but before publish with force=false", func(t *testing.T) {
		dir := t.TempDir()
		input := filepath.Join(dir, "input.mp4")
		if err := os.WriteFile(input, []byte("input"), 0o600); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, "final.mp4")
		cfg := config.Default()
		cfg.Input = input
		cfg.Output = dst
		cfg.Force = false

		// 1. Preflight passes when destination is absent
		if err := Preflight(cfg); err != nil {
			t.Fatalf("preflight failed unexpectedly: %v", err)
		}

		// 2. Another process creates the destination file between preflight and publish
		concurrentContent := []byte("concurrently created file content")
		if err := os.WriteFile(dst, concurrentContent, 0o600); err != nil {
			t.Fatal(err)
		}

		// 3. Render creates temporary output
		src := filepath.Join(dir, "temp.mp4")
		if err := os.WriteFile(src, []byte("my new render"), 0o600); err != nil {
			t.Fatal(err)
		}

		// 4. Publish must refuse to overwrite the newly created destination
		err := Publish(src, dst, cfg.Force)
		if err == nil {
			t.Fatal("expected publish to fail when destination appeared after preflight, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("unexpected error message: %v", err)
		}

		// 5. Destination contents must remain untouched
		currentDst, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(currentDst) != string(concurrentContent) {
			t.Fatalf("concurrent file was overwritten: got %q, want %q", string(currentDst), string(concurrentContent))
		}
	})
}
