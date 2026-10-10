# Lecture Jet

Linux CLI tool for conservatively removing silence from recorded lectures.

## Ownership

- `cmd/lecturejet/`: CLI entrypoint (`main.go`), flag definitions, argument
  parsing, help/version (`cli.go`).
- `internal/config/`: Configuration defaults, validation, and shared option
  types.
- `internal/ui/`: Interactive VAAPI GPU discovery and configuration wizard.
- `internal/analyze/`: Audio silence detection, window metrics, and parallel
  worker streaming.
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
  directory (`.output.mp4.lecturejet-*.mp4`). Require `--force` to overwrite
  existing files, and publish atomically via rename.
- **Style & Compatibility**: Format Go code with tabs (`gofmt -w`). Maintain
  usable non-CGO stubs for `CGO_ENABLED=0` builds.

## Disclosed Reference

- **Native C & CGO Bridge**: Consult [`native/AGENTS.md`](native/AGENTS.md)
  when editing `native/` or `internal/media/` for FFmpeg ownership, CGO memory,
  and VAAPI encoding rules.
- **Audio Pipeline & Timeline**: Consult [`internal/AGENTS.md`](internal/AGENTS.md)
  when editing `internal/analyze/` or `internal/timeline/` for 48 kHz alignment,
  PTS mapping, and filter rules.

## Verification

Run focused checks during development, then execute all verification targets
before committing:

```bash
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
