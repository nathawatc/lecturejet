# Native Guidelines

Guidelines for FFmpeg C code and CGO bridge. Back to [AGENTS.md](../AGENTS.md).

## Module Layout

- `include/`: `lecturejet.h` (minimal public C/cgo API) and `types.h` (internal pipeline state).
- `config/`: `encoder_profile.{h,c}` (SSOT for VAAPI HEVC/H.264 and AAC encoder parameters).
- `hw/`: `vaapi_device.{h,c}` (hardware device and frame context management).
- `filter/`: `filtergraph.{h,c}` (hardware upload, scale_vaapi, format negotiation).
- `pipeline/`: Modular streaming pipeline
  (`demux.c`, `video_pipeline.c`, `audio_pipeline.c`, `drain.c`, `engine.c`).
- `audio/`: Modular audio decoder, resampler, and RNNoise (`audio.h`, `decoder.c`, `resample.c`, `rnnoise.c`).
- `timeline/`: `timeline.{h,c}` (source-to-output timestamp and sample mapping).
- `probe/`: `probe.c` (media container and stream capability probing).
- `common/`: Shared helpers (`common.h`, `errors.c`, `cancel.c`, `progress.c`, `samples.c`).
- `lecturejet.c`: Unity compilation unit including all module sources for cgo.

## Resource Ownership

- **Explicit Lifecycle**: Every `AVFrame`, `AVPacket`, `AVFormatContext`,
  `AVCodecContext`, `AVFilterGraph`, and `SwrContext` has
  exactly one deterministic cleanup path.
- **Error Cleanup**: In error paths, free partially allocated contexts
  immediately (`goto done` or `goto fail` with centralized `NULL`-safe frees).
- **C.CString Ownership**: Every `C.CString` allocated in Go must be freed
  with `C.free` in the same function scope via `defer`.
- **Pointer Pinning**: Do not retain unpinned Go pointers across C calls or
  store Go pointers in C structures.
- **Bounded Error Buffers**: All native C entrypoint functions (`lj_probe_*`,
  `lj_audio_*`, `lj_render_*`) must NUL-terminate `error` on
  entry (`error[0] = '\0'`) and write bounded messages via `snprintf`/`lj_error`.
  Propagate native FFmpeg return codes whenever available.

## Video & VAAPI

- **Hardware/Software Decode + VAAPI Upload**: Decode source frames using VAAPI
  hardware acceleration when supported, falling back to multi-threaded software
  decode and `NV12` upload if the input codec or profile is unsupported.
  Hardware-decoded frames pass directly to the VAAPI encoder on GPU VRAM.
- **Hardware Surface Scaling**: Process frame scaling and format negotiation
  directly on the GPU using `scale_vaapi` in `filter/filtergraph.c`, bypassing
  host CPU scaling.
- **Decoupled Encode Queue**: Drain encoded packets opportunistically on
  `AVERROR(EAGAIN)` or when hardware surface pools are depleted in
  `pipeline/video_pipeline.c`. Avoid per-frame lockstep draining to saturate GPU
  encoder queues.
- **Strict Configuration**: Fail with diagnostics if an explicitly requested
  VAAPI device or codec is unsupported; never fall back silently.
- **HEVC Codec Tags**: Tag HEVC streams as `hev1` or `hvc1` directly during
  render (`codecpar->codec_tag`), avoiding secondary remux passes.
- **Keyframe Seeking Over Silence**: On silent gaps >= 3.0 seconds, seek backward
  to the keyframe before the next speech segment and discard pre-roll frames,
  bypassing full linear decode. Short gaps (< 3.0s) remain linearly decoded.
- **Single-Pass Interleaved Demux**: Demux input audio and video packets in a
  single pass (`av_read_frame`), interleaving encoded output packets without
  reading media files multiple times.

## Diagnostics & Standards

- **Strict Compilation**: All C module sources must pass `-Wall -Wextra -Werror -pedantic -std=gnu11`
  cleanly via `make native-check`.
- **Centralized Profiles**: All VAAPI HEVC/H.264 and AAC encoder parameters
  (bitrates, GOP structure, compression levels, B-frame limits) reside exclusively
  in `config/encoder_profile.c`.

## Cancellation & Concurrency

- **Atomic Interruption**: Wire `LJCancelToken` into FFmpeg format contexts
  via `format->interrupt_callback`.
- **Clean Shutdown**: Context cancellation must terminate native I/O loops
  promptly and unblock Go goroutines without leaking either side.
- **Non-CGO Fallbacks**: Keep package builds and unit tests working under
  `CGO_ENABLED=0` through `internal/media/media_stub.go`.
