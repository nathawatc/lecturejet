# Lecture Jet

Linux CLI tool for removing silence from recorded lectures while preserving natural speech cadence.

## Key Features

- **Speech-First Trimming**: Prioritizes natural speech cadence over aggressive cutting.
- **Hardware-Accelerated Encoding**: Fast H.264 and HEVC encoding via Linux VAAPI, featuring hardware-accelerated.
- **Vectorized Audio Analysis**: High-throughput audio window analysis accelerated with AVX2 and FMA3.
- **Embedded Neural Denoising**: Built-in RNNoise (`arnndn`) suppression with embedded neural weights.
- **Fast Gap Seeking**: Bypasses full linear video decoding by seeking over silent gaps.
- **Interactive Wizard & Terminal Dashboard**: Displaying real-time EMA ETA progress, cut counters.
- **Atomic Publication**: Commits via atomic rename, preventing damaged files on cancel or crash.

## Installation

### System Requirements

- **Operating System**: Linux (kernel 3.15+ for `memfd_create` and atomic file replacement)
- **Hardware**: GPU with VAAPI encoding support (Intel, AMD)
- **Go**: 1.26 or newer
- **FFmpeg**: 6.0+ (`libavformat`, `libavcodec`, `libavutil`, `libswresample`, `libavfilter`) and `libva`

### Distribution Packages

#### Arch Linux

```bash
sudo pacman -S --needed go ffmpeg libva-mesa-driver make pkgconf
```

#### Ubuntu / Debian

```bash
sudo apt update
sudo apt install -y golang-go ffmpeg libavcodec-dev libavformat-dev \
    libavutil-dev libswresample-dev libavfilter-dev libva-dev \
    libva-drm2 make pkg-config
```

Ensure your user account has permission to access the GPU render node:

```bash
ls -l /dev/dri/renderD128
sudo usermod -aG render $USER
# Log out and back in for group changes to take effect
```

### Build from Source

```bash
make build
```

The compiled binary will be placed at `build/lecturejet`.

## Usage & Quickstart

### Basic Usage

Render an input video with default automatic output naming:

```bash
./build/lecturejet lecture.mp4
```

This generates `lecture.out.mp4` by default.

Specify an explicit output file:

```bash
./build/lecturejet lecture.mp4 processed.mp4
```

In-place atomic replacement with `--force` (overwrites input file directly):

```bash
./build/lecturejet --force lecture.mp4
```

### Silence Analysis Only

Scan an input video, compute window metrics, and report detected cuts without encoding:

```bash
./build/lecturejet analyze lecture.mp4
```

### Non-Interactive / Scripting Mode

Bypass the interactive GPU and audio wizard by specifying `--non-interactive`:

```bash
./build/lecturejet --non-interactive --video-codec hevc lecture.mp4
```

## CLI Reference

### Render Options (`lecturejet <input.mp4> [output.mp4] [options]`)

| Flag                 | Default | Description                                                     |
| -------------------- | ------- | --------------------------------------------------------------- |
| `--video-codec`      | `hevc`  | Video encoder codec: `hevc` or `h264`                           |
| `--hevc-tag`         | `hev1`  | HEVC codec tag: `hev1` (universal) or `hvc1` (Apple compatible) |
| `--vaapi-device`     | auto    | VAAPI render node path (e.g., `/dev/dri/renderD128`)            |
| `--rnnoise`          | `false` | Enable RNNoise neural background noise suppression              |
| `--rnnoise-strength` | `100`   | RNNoise suppression strength (`0` to `100`)                     |
| `--mono`             | `false` | Downmix audio to mono (automatically enabled with `--rnnoise`)  |
| `--stereo`           | `false` | Force stereo output channel retention                           |
| `--threshold`        | `-38`   | Silence detection threshold in dBFS                             |
| `--min-silence`      | `450`   | Minimum silence duration in milliseconds before cutting         |
| `--margin`           | `150`   | Speech safety margin in milliseconds before and after cuts      |
| `--threads`          | `auto`  | Video decoder thread count or auto                              |
| `--force`            | `false` | Overwrite destination file or allow in-place input replacement  |
| `--non-interactive`  | `false` | Skip interactive wizard and use flags/defaults                  |
| `-v`, `--verbose`    | `false` | Enable verbose demuxer, seeker, and filter logging              |

### Analysis Options (`lecturejet analyze <input.mp4> [options]`)

| Flag                 | Default | Description                                       |
| -------------------- | ------- | ------------------------------------------------- |
| `--threshold`        | `-38`   | Silence detection threshold in dBFS               |
| `--min-silence`      | `450`   | Minimum silence duration in milliseconds          |
| `--margin`           | `150`   | Speech safety margin in milliseconds              |
| `--rnnoise`          | `false` | Evaluate silence detection after applying RNNoise |
| `--rnnoise-strength` | `100`   | RNNoise suppression strength (`0` to `100`)       |
| `--threads`          | `auto`  | Decoder thread count or auto                      |
| `-v`, `--verbose`    | `false` | Enable detailed logging                           |

## Verification & Testing

Execute repository verification targets:

```bash
make native-check   # Validate C sources with -Wall -Wextra -Werror -pedantic -std=gnu11
make test           # Run standard unit tests
make test-race      # Run data race detection tests
make vet            # Static analysis via go vet
make bench          # Run SIMD analysis and timeline benchmarks
make integration    # Run end-to-end synthetic media test suite
```

## Troubleshooting

- **`no usable VAAPI encoder`**:
  Ensure your GPU drivers are installed and verify that FFmpeg detects VAAPI encoders:

  ```bash
  ffmpeg -hide_banner -encoders | grep vaapi
  ```

- **`opening VAAPI device ... permission denied`**:
  Add your user to the appropriate group:

  ```bash
  sudo usermod -aG render $USER
  ```

- **`encoder is unavailable for selected VAAPI device`**:
  Your GPU hardware may not support the selected codec profile (e.g. HEVC 10-bit on older hardware).

## Known Limitations

- **Linux Platform**: Requires Linux kernel 3.15+ for `memfd_create` and atomic file rename guarantees.
- **Hardware Acceleration**: Video encoding requires a VAAPI-compatible GPU device.
- **Container Format**: Input and output containers must be MP4 with AAC audio encoding.
