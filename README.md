# Lecture Jet

Removes silence from recorded lectures while preserving natural speech cadence.

## Key Features

- **Conservative Trimming**: Prioritizes speech preservation over reduction.
- **Hardware-Accelerated Encoding**: Fast H.264 and HEVC encoding via VAAPI.
- **Interactive Wizard**: Automatically probes your GPU for available encoders.
- **Optional RNNoise Denoising**: Filters out background noise.

## Installation

### System Requirements

- **OS**: Linux (kernel 3.15+ for atomic replacement support)
- **Hardware**: GPU with VAAPI support
- **Go**: 1.26 or newer
- **FFmpeg**

### Arch Linux

```bash
sudo pacman -S --needed go ffmpeg libva-mesa-driver make pkgconf
```

Ensure your user account has read permission for the render node:

```bash
ls -l /dev/dri/renderD128
```

### Build from Source

```bash
make build
```

The compiled binary will be placed at `build/lecturejet`.

## Quickstart

Run `lecturejet` with an input file and the desired output file:

```bash
lecturejet input.mp4 output.mp4
```

This will probe your hardware, choose an available video encoder,
and ask whether to enable noise reduction and mono audio downmixing.

For more options, see `lecturejet -h`.

> [!NOTE]
> Output files must have the `.mp4` extension.
> Existing files are protected by default unless `--force` is passed.

### Noise Suppression (RNNoise)

Integrates FFmpeg's `arnndn` filter for noise reduction.

- `--rnnoise`: Enable noise reduction (requires `--mono`).
- `--rnnoise-model <path>`: Path to a compatible `.rnnn` model file.
- `--rnnoise-strength <0-100>`: Suppression strength (default: `80`).

## Troubleshooting

- **`no usable VAAPI encoder`**:
  Ensure your GPU drivers are installed and verify that FFmpeg detects VAAPI encoders:

  ```bash
  ffmpeg -hide_banner -encoders | grep vaapi
  ```

- **`opening VAAPI device ... permission denied`**:
  Add your user to the appropriate group (e.g., `sudo usermod -aG render $USER`).

- **`encoder is unavailable for selected VAAPI device`**:
  Your GPU may not support the selected codec.

## Known Limitations

- Requires Linux and a VAAPI-compatible GPU for video encoding.
- The output container must be MP4 with AAC audio.
- RNNoise requires an externally provided `.rnnn` model file.
