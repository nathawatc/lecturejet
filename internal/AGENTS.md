# Internal Pipeline Guidelines

Guidelines for analysis, audio models, timeline mapping, and app workflow.
Back to [AGENTS.md](../AGENTS.md).

## Analysis & Audio

- **Conservative Reduction**: Preserve speech before maximizing cuts. Short
  natural pauses remain intact unless they exceed configured silence policy.
- **48 kHz Boundary Alignment**: Resample decoded audio to 48 kHz mono for
  silence detection. Align analyzed timestamps to integer 48 kHz sample
  boundaries.
- **Vectorized Analysis**: Accelerate RMS energy, peak scanning, and zero-crossing
  rate with AVX2 and FMA3 kernels on `amd64` (`rms_amd64.s`, `peak_amd64.s`).
  Maintain strict output parity with `rms_generic.go` portable fallbacks.
- **Embedded Audio Models**: Package neural weights in `internal/audio/assets/std.rnnn`.
  Expose to FFmpeg via `memfd_create` anonymous RAM descriptors (`/proc/self/fd/<fd>`)
  on Linux to avoid temporary files on disk.
- **Sequential Recurrent Filters**: Maintain state sequentially for recurrent
  filters such as `arnndn`; process an entire audio stream through a continuous
  filtergraph without splitting across parallel instances.
- **Bounded Streaming**: Chunk incoming PCM through `pcmBlockSplitter` in
  fixed blocks (`96,000` samples); keep worker job and result queues bounded.

## Timeline Mapping

- **Single Source of Truth**: Keep timeline calculations in `internal/timeline`;
  rendering and analysis must not duplicate timestamp mapping logic.
- **Immutable Timeline**: Construct `timeline.Timeline` once through
  `timeline.New` or `timeline.FromCuts`; generate modifications by instantiating
  new timelines.
- **Presentation Timestamps**: Map video frames strictly using presentation
  timestamps (PTS / `best_effort_timestamp`); never substitute packet decode
  timestamps (DTS).

## Application Workflow & UI

- **Multi-Phase Progress**: Propagate phase context via `WithWorkflow`. Terminal
  progress reporters calculate EMA throughput, time remaining, and OSC 9;4 progress
  escape codes while preserving quiet periodic logs on non-TTY streams.
- **Safe Publication**: Always render output to a hidden temporary file in the target
  directory (`.output.mp4.lecturejet-*.mp4`). Enforce `--force` overwrite guards
  prior to encoding and commit atomically via filesystem rename.
- **Terminal Wizard**: Probe available VAAPI devices via `/dev/dri/renderD*`,
  validate hardware codecs interactively, and default to embedded RNNoise at 100%
  strength with automatic mono downmixing.
