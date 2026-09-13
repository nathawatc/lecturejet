package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"codeberg.org/nathawat/lecturejet/internal/config"
)

// Preflight validates filesystem-dependent properties of cfg before expensive probing and media processing.
func Preflight(cfg config.Config) error {
	inClean := filepath.Clean(cfg.Input)
	outClean := filepath.Clean(cfg.Output)
	inAbs, inErr := filepath.Abs(inClean)
	outAbs, outErr := filepath.Abs(outClean)
	if (inErr == nil && outErr == nil && inAbs == outAbs) || inClean == outClean {
		return fmt.Errorf("input and output must not refer to the same file: %s", cfg.Input)
	}

	outDir := filepath.Dir(cfg.Output)
	dirInfo, err := os.Stat(outDir)
	if err != nil {
		return fmt.Errorf("output directory %s: %w", outDir, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("output directory is not a directory: %s", outDir)
	}

	outInfo, outErr := os.Lstat(cfg.Output)
	if outErr == nil {
		if outInfo.IsDir() {
			return fmt.Errorf("output path is a directory: %s", cfg.Output)
		}
		if inInfo, inErr := os.Stat(cfg.Input); inErr == nil {
			if statOut, err := os.Stat(cfg.Output); err == nil && os.SameFile(inInfo, statOut) {
				return fmt.Errorf("input and output refer to the same file: %s", cfg.Input)
			}
		}
		if !cfg.Force {
			return fmt.Errorf("output file already exists: %s (use --force to overwrite)", cfg.Output)
		}
	} else if !errors.Is(outErr, os.ErrNotExist) {
		return fmt.Errorf("checking output path %s: %w", cfg.Output, outErr)
	}

	return nil
}

// Publish atomically moves src to dst. If force is false, dst is guaranteed not to be overwritten.
func Publish(src, dst string, force bool) error {
	if force {
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("publishing output: %w", err)
		}
		return nil
	}

	if err := renameNoReplace(src, dst); err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("output file already exists: %s (use --force to overwrite): %w", dst, os.ErrExist)
		}
		return fmt.Errorf("publishing output: %w", err)
	}
	return nil
}
