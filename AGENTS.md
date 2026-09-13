# Repository Guidelines

**Updated:** 2026-09-13

Lecture Jet is a Linux CLI for conservatively removing silence from
recorded lectures.

## Ownership

- `cmd/lecturejet/`: CLI entrypoint, argument parsing, help/version,
  and interactive wizard dispatch
- `internal/config/`: configuration defaults, validation, and shared
  option types
- `internal/ui/`: interactive VAAPI, RNNoise, and audio-mode selection
- `internal/analyze/`: streaming PCM analysis, window metrics, worker
  coordination, and silence detection
- `internal/timeline/`: immutable keep/cut segments and source-to-output
  timestamp mapping
- `internal/media/`: Go/CGO boundary, probing, audio decoding,
  cancellation, progress, and render dispatch
- `internal/app/`: top-level analyze/render workflows, output preflight,
  temporary-file lifecycle, and publication
- `native/lecturejet_ffmpeg.c`: FFmpeg decode/filter/render and VAAPI
  encoding
- `native/lecturejet_ffmpeg.h`: native bridge contract consumed by CGO
- `scripts/generate_synthetic.sh`: deterministic FFmpeg-generated media
- `scripts/integration.sh`: end-to-end H.264, HEVC, stereo, and output
  validation
- `Makefile`: supported build, test, vet, benchmark, integration, and
  cleanup entrypoints

## Rules

- Preserve speech before maximizing reduction; short natural pauses are
  intentional unless they satisfy the configured silence policy.
- Keep media processing streaming and bounded. Do not buffer entire audio
  or video streams, or unbounded analysis results, in memory.
- Preserve sequential state for recurrent filters such as FFmpeg `arnndn`;
  do not split one stream across independent filter instances.
- Decode video in software, normalize frames as needed, and upload NV12
  frames to VAAPI for hardware encoding.
- Never silently replace an explicitly requested VAAPI device or codec.
  Unsupported explicit choices must fail with a useful diagnostic.
- Keep timeline math in `internal/timeline`; rendering and analysis code
  must not duplicate source/output timestamp mapping policy.
- Keep `timeline.Timeline` immutable after construction. Build adjusted
  timelines through the existing constructors.
- Align analyzed audio timing to integer 48 kHz sample boundaries.
- Use presentation timestamps for video mapping; do not substitute packet
  DTS for decoded-frame presentation time.
- Keep native FFmpeg ownership explicit. Every allocated frame, packet,
  buffer, filter object, codec context, and format context must have one
  clear cleanup path.
- Every `C.CString` allocated by Go must be freed in the same ownership
  scope.
- Do not retain unpinned Go pointers in C.
- Keep native error buffers bounded and NUL-terminated; propagate the
  original FFmpeg/libav error whenever possible.
- Context cancellation must terminate native I/O and any Go cancellation
  monitor goroutine without leaking either side.
- Render into a temporary `.mp4` in the destination directory and publish
  only after the render completes successfully.
- Without `--force`, publication must not replace an existing destination.
  With `--force`, replacement must remain atomic.
- Output files are MP4 with AAC audio; video encoding requires VAAPI.
- RNNoise requires mono output and an externally supplied compatible
  `.rnnn` model.
- HEVC `hvc1` output is produced by lossless remux after encoding; do not
  re-encode solely to change the codec tag.
- Synthetic integration media must be generated with
  `scripts/generate_synthetic.sh`; do not commit generated `.mp4` fixtures.
- Capability-based integration skips are allowed only when the capability
  itself is unavailable. Once a supported path is selected, Lecture Jet
  render failures are test failures.
- Keep `CGO_ENABLED=0` package builds/tests usable through the existing
  non-CGO stubs.

## Verification

Run the relevant focused tests while developing, then use the repository
targets before committing:

```bash
make test
make test-race
make vet
make build
make integration
```

Use `make bench` when changing analysis or timeline hot paths.

Before committing, also check:

```bash
gofmt -w <changed Go files>
```

Native changes should build cleanly through `make build`; do not validate
the C bridge only through editor diagnostics.
