# Lecture Jet

Linux CLI tool for conservatively removing silence from recorded lectures.

## Ownership

- `cmd/lecturejet/`: CLI entrypoint (`main.go`), flag definitions, argument
  parsing, help/version (`cli.go`).
- `internal/config/`: Configuration defaults, validation, and shared option
  types.
- `internal/ui/`: Interactive VAAPI GPU discovery and configuration wizard.
- `internal/analyze/`: Audio silence detection, SIMD window metrics, and parallel
  worker streaming.
- `internal/audio/`: Embedded RNNoise neural model assets and memory-backed
  descriptor lifecycle.
- `internal/timeline/`: Immutable timeline segments and source-to-output
  timestamp mapping.
- `internal/media/`: Go/CGO boundary, probing, audio decoding, and render
  dispatch.
- `internal/app/`: Analyze and render workflows, preflight checks, temporary
  file lifecycle, and publication.
- `native/`: Modular FFmpeg VAAPI encoder, audio decoder, and filtergraph
  implementation.
- `scripts/`: Synthetic fixture generator (`generate_synthetic.sh`) and
  end-to-end integration suite (`integration.sh`).
- `Makefile`: Build, test, race, vet, bench, and integration targets.

## Core Rules

- **Speech First**: Prioritize preserving speech over maximizing cut duration.
  Short pauses are intentional cadence.
- **Bounded Streaming**: Stream all media in bounded chunks. Never buffer full
  streams or unbounded metrics in memory.
- **Atomic Publication**: Render to a temporary file in the destination
  directory (`.output.mp4.lecturejet-*.mp4`). Support automatic output naming
  (`<input>.out.mp4`) and require `--force` to overwrite existing destinations
  or replace input in-place. Publish atomically via rename.
- **Style & Compatibility**: Format Go code with tabs (`gofmt -w`). Maintain
  usable non-CGO stubs for `CGO_ENABLED=0` builds.

## Disclosed Reference

- **Native C & CGO Bridge**: Consult [`native/AGENTS.md`](native/AGENTS.md)
  when editing `native/` or `internal/media/` for FFmpeg ownership, CGO memory,
  hardware scaling, decoupled encoding, and VAAPI pipeline rules.
- **Audio Pipeline & Timeline**: Consult [`internal/AGENTS.md`](internal/AGENTS.md)
  when editing `internal/analyze/`, `internal/audio/`, `internal/app/`, or
  `internal/timeline/` for 48 kHz alignment, AVX2 SIMD metrics, embedded models,
  PTS mapping, and progress reporting.

## Verification

Run focused checks during development, then execute all verification targets
before committing:

```bash
make native-check
make test
make test-race
make vet
make bench
make build
make integration
CGO_ENABLED=0 go test ./cmd/lecturejet ./internal/...
gofmt -w <changed Go files>
```

All targets must pass with zero failures and zero warnings.
