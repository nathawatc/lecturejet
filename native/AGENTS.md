# Native Guidelines

Guidelines for FFmpeg C code and CGO bridge. Back to [AGENTS.md](../AGENTS.md).

## Resource Ownership

- **Explicit Lifecycle**: Every `AVFrame`, `AVPacket`, `AVFormatContext`,
  `AVCodecContext`, `AVFilterGraph`, `SwrContext`, and `SwsContext` has
  exactly one deterministic cleanup path.
- **Error Cleanup**: In error paths, free partially allocated contexts
  immediately (`goto done` or `goto fail` with centralized `NULL`-safe frees).
- **C.CString Ownership**: Every `C.CString` allocated in Go must be freed
  with `C.free` in the same function scope via `defer`.
- **Pointer Pinning**: Do not retain unpinned Go pointers across C calls or
  store Go pointers in C structures.
- **Bounded Error Buffers**: All native C entrypoint functions (`lj_probe_*`,
  `lj_audio_*`, `lj_render_*`, `lj_remux_*`) must NUL-terminate `error` on
  entry (`error[0] = '\0'`) and write bounded messages via `snprintf`/`lj_error`.
  Propagate native FFmpeg return codes whenever available.

## Video & VAAPI

- **Hardware/Software Decode + VAAPI Upload**: Decode source frames using VAAPI
  hardware acceleration when supported, falling back to multi-threaded software
  decode and `NV12` scaling if the input codec or profile is unsupported.
  Hardware decoded frames pass directly to the VAAPI encoder on GPU VRAM.
- **Strict Configuration**: Fail with diagnostics if an explicitly requested
  VAAPI device or codec is unsupported; never fall back silently.
- **Lossless HEVC Remux**: Produce `hvc1` tagged HEVC output through stream-copy
  remux (`lj_remux_hevc_hvc1`); do not re-encode solely to modify fourcc tag.

## Cancellation & Concurrency

- **Atomic Interruption**: Wire `LJCancelToken` into FFmpeg format contexts
  via `format->interrupt_callback`.
- **Clean Shutdown**: Context cancellation must terminate native I/O loops
  promptly and unblock Go goroutines without leaking either side.
- **Non-CGO Fallbacks**: Keep package builds and unit tests working under
  `CGO_ENABLED=0` through `internal/media/media_stub.go`.
