# Internal Pipeline Guidelines

Guidelines for analysis, silence detection, and timeline mapping.
Back to [AGENTS.md](../AGENTS.md).

## Analysis & Audio

- **Conservative Reduction**: Preserve speech before maximizing cuts. Short
  natural pauses remain intact unless they exceed configured silence policy.
- **48 kHz Boundary Alignment**: Resample decoded audio to 48 kHz mono for
  silence detection. Align analyzed timestamps to integer 48 kHz sample
  boundaries.
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
