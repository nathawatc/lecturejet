# Lecture Jet

Linux CLI for conservative lecture-video shortening.

## Philosophy

Speech preservation beats reduction. Defaults: 10 ms windows, -38 dBFS silence
threshold, 450 ms minimum silence, 150 ms speech margin, 4 dB hysteresis.
Natural 100–300 ms pauses normally survive.

## Dependencies

Arch Linux packages:

```bash
sudo pacman -S --needed go ffmpeg libva-mesa-driver make pkgconf
```

`pkg-config` must discover FFmpeg development headers and libraries:

```bash
pkg-config --cflags --libs libavformat libavcodec libavutil libswresample
libswscale libavfilter
```

VAAPI needs usable render node such as `/dev/dri/renderD128`, working
Mesa/vendor driver, and permission to open it. `vainfo` helps troubleshooting
but is optional.

## Build and test

```bash
make build
make test
make vet
make test-race
make bench
```

Binary: `build/lecturejet`.

## Usage

Basic invocation opens wizard in terminal:

```bash
lecturejet input.mp4 output.mp4
```

Wizard offers only VAAPI codecs successfully probed for input dimensions, then
asks about RNNoise and mono output. Flags resolve choices for automation:

```bash
lecturejet lecture.mp4 output.mp4 \
  --video-codec hevc \
  --hevc-tag hvc1 \
  --vaapi-device /dev/dri/renderD128 \
  --rnnoise --rnnoise-strength 80 --rnnoise-model /path/to/model.rnnn \
  --mono --threads auto --non-interactive
```

`--stereo` produces two-channel output. `--mono` default. Codec values:
`hevc`, `h264`. HEVC tags: `--hevc-tag hev1` (default) or `hvc1` for Apple
compatibility.

Analysis-only mode:

```bash
lecturejet analyze lecture.mp4 --threads auto
```

Silence controls:

```text
--threshold DBFS       default -38
--min-silence MS       default 450
--margin MS            default 150
--hysteresis DB        default 4
```

`--threads auto` selects up to eight ordered batch workers. Bounded queues stream
PCM; memory stays bounded regardless of lecture duration.

## RNNoise

Optional RNNoise uses FFmpeg's public `arnndn` filter. Provide compatible `.rnnn`
model with `--rnnoise-model`; no model bundled. Strength is 0–100 and maps to
filter mix; 80 is lecture default, with 75–100 suitable for strong lecture
denoising. Missing model or unavailable filter is an
explicit error.

Analysis and rendering use same processed audio path. One sequential decoder
preserves filter state; blocks never split across independent recurrent filter
instances.

## Hardware and timestamp behavior

Native bridge uses public FFmpeg APIs for demux, decode, resample, VAAPI frame
upload, encode, and mux. Video decodes in software and uploads NV12 frames to
VAAPI; this is the correctness-first fallback path. Audio cuts split at integer
48 kHz sample boundaries. Video frames use display timestamps
(`best_effort_timestamp`), not packet DTS; dropped-frame gaps map through the
compact timeline.

Output writes to same-directory temporary `.mp4`, then atomically renames after
trailer writing succeeds. HEVC VAAPI encoding is muxed as `hev1`; requested
`hvc1` uses a lossless stream-copy remux before the final atomic rename.
Interrupt/cancel paths never present temporary output as final. VAAPI video uses
constant-QP 28 by default, so bitrate follows scene complexity instead of
forcing every lecture through a 6 Mb/s stream.

## Synthetic integration test

Script creates deterministic mono/48 kHz, stereo/44.1 kHz, 30 fps, and 29.97 fps
media without external/copyrighted input:

```bash
make integration
```

H.264 VAAPI test skips only without render node. HEVC reports skip when selected
device rejects it. Both outputs are probed and decoded when available.

## Troubleshooting

- `no usable VAAPI encoder`: check `/dev/dri/renderD*`, user groups, vendor driver, and `ffmpeg -hide_banner -encoders | grep vaapi`.
- `opening VAAPI device ... permission denied`: grant render-device access or choose another `--vaapi-device`.
- `encoder is unavailable for selected VAAPI device`: use wizard codec; hardware capability is device-specific.
- `opening RNNoise ... model path is required`: pass `--rnnoise-model /path/to/model.rnnn`.
- Native FFmpeg errors include operation and library detail. LectureJet never silently changes explicitly selected codec.

## Known limitations

v0.1 requires VAAPI video output and AAC audio. Hardware decode zero-copy is not
enabled; software decode plus VAAPI upload is used. AAC may add one encoder
frame of padding (normally <= 21.3 ms at 48 kHz); frame cadence may make video
duration differ from continuous timeline by up to one source frame. RNNoise
requires an externally supplied model.
