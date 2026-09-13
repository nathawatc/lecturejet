#!/usr/bin/env bash
set -euo pipefail

binary=${1:-./build/lecturejet}
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

"$(dirname "$0")/generate_synthetic.sh" "$work_dir"
input="$work_dir/synthetic-mono-48k.mp4"
output="$work_dir/output-h264.mp4"
stereo_input="$work_dir/synthetic-stereo-44k.mp4"
stereo_probe=$(ffprobe -v error -select_streams a:0 -show_entries stream=sample_rate,channels \
  -of default=nw=1 "$stereo_input")
grep -q '^sample_rate=44100$' <<<"$stereo_probe"
grep -q '^channels=2$' <<<"$stereo_probe"

device=${LECTUREJET_VAAPI_DEVICE:-}
if [[ -z "$device" ]]; then
  for candidate in /dev/dri/renderD*; do
    [[ -e "$candidate" ]] || continue
    device=$candidate
    break
  done
fi
if [[ -z "$device" ]]; then
  echo "SKIP VAAPI integration: no /dev/dri/renderD* device" >&2
  exit 0
fi

"$binary" "$input" "$output" --non-interactive --vaapi-device "$device" \
  --video-codec h264 --mono --threads 2
ffprobe -hide_banner -v error -show_streams -show_format "$output" >/dev/null
ffmpeg -hide_banner -v error -i "$output" -map 0:v:0 -f null -
ffmpeg -hide_banner -v info -i "$output" -map 0:a:0 -af astats=metadata=1:reset=0 -f null - 2>"$work_dir/audio.log"
awk -F': ' '/Number of NaNs:/ { if (($2 + 0) != 0) exit 1 } /Number of Infs:/ { if (($2 + 0) != 0) exit 1 }' "$work_dir/audio.log"

video_duration=$(ffprobe -v error -select_streams v:0 -show_entries stream=duration -of csv=p=0 "$output")
audio_duration=$(ffprobe -v error -select_streams a:0 -show_entries stream=duration -of csv=p=0 "$output")
awk -v video="$video_duration" -v audio="$audio_duration" 'BEGIN { difference=video-audio; if (difference < 0) difference=-difference; if (difference > 0.1) exit 1 }'
ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts -of csv=p=0 "$output" |
  awk -F, 'BEGIN { last_pts=-1; last_dts=-1 } $1 != "N/A" && $1 < last_pts { exit 1 } $2 != "N/A" && $2 < last_dts { exit 1 } { if ($1 != "N/A") last_pts=$1; if ($2 != "N/A") last_dts=$2 }'

echo "H.264 VAAPI integration passed: A/V duration delta <= 100ms"

stereo_output="$work_dir/output-stereo.mp4"
"$binary" "$stereo_input" "$stereo_output" --non-interactive --vaapi-device "$device" \
  --video-codec h264 --stereo --threads 2
[[ -s "$stereo_output" ]]
ffprobe -hide_banner -v error -show_streams -show_format "$stereo_output" >/dev/null
ffmpeg -hide_banner -v error -i "$stereo_output" -f null -
stereo_out_probe=$(ffprobe -v error -select_streams a:0 -show_entries stream=sample_rate,channels \
  -of default=nw=1 "$stereo_output")
grep -q '^channels=2$' <<<"$stereo_out_probe"
grep -q '^sample_rate=48000$' <<<"$stereo_out_probe"
stereo_video_duration=$(ffprobe -v error -select_streams v:0 -show_entries stream=duration -of csv=p=0 "$stereo_output")
stereo_audio_duration=$(ffprobe -v error -select_streams a:0 -show_entries stream=duration -of csv=p=0 "$stereo_output")
awk -v video="$stereo_video_duration" -v audio="$stereo_audio_duration" 'BEGIN { difference=video-audio; if (difference < 0) difference=-difference; if (difference > 0.1) exit 1 }'

echo "Stereo VAAPI integration passed: channels=2, sample_rate=48000"

input_width=$(ffprobe -v error -select_streams v:0 -show_entries stream=width -of csv=p=0 "$input")
input_height=$(ffprobe -v error -select_streams v:0 -show_entries stream=height -of csv=p=0 "$input")
[[ -n "$input_width" && -n "$input_height" ]]

hevc_probe_log="$work_dir/hevc_probe.log"
hevc_supported=0
if ffmpeg -hide_banner -loglevel verbose -init_hw_device "vaapi=hw:$device" -filter_hw_device hw \
    -f lavfi -i "color=s=${input_width}x${input_height}:d=0.04" -vf "format=nv12,hwupload" \
    -c:v hevc_vaapi -f null - >"$hevc_probe_log" 2>&1; then
  hevc_supported=1
else
  if grep -q -i -E "unknown encoder|error while opening encoder|function not implemented|no matching.*profile|failed to create encode pipeline|does not support|not supported|unsupported profile|encoder not found|vacreateconfig|vacreatecontext" "$hevc_probe_log"; then
    echo "SKIP hevc_vaapi integration: encoder/device rejected HEVC" >&2
  else
    echo "ERROR: hevc_vaapi capability probe failed unexpectedly:" >&2
    cat "$hevc_probe_log" >&2
    exit 1
  fi
fi

if [[ "$hevc_supported" -eq 1 ]]; then
  hevc_output="$work_dir/output-hevc.mp4"
  "$binary" "$input" "$hevc_output" --non-interactive --vaapi-device "$device" \
    --video-codec hevc --mono --threads 2
  hevc_tag=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_tag_string -of csv=p=0 "$hevc_output")
  [[ "$hevc_tag" == "hev1" ]]
  ffmpeg -hide_banner -v error -i "$hevc_output" -map 0:v:0 -f null -
  hvc1_output="$work_dir/output-hevc-hvc1.mp4"
  "$binary" "$input" "$hvc1_output" --non-interactive --vaapi-device "$device" \
    --video-codec hevc --hevc-tag hvc1 --mono --threads 2
  hvc1_tag=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_tag_string -of csv=p=0 "$hvc1_output")
  [[ "$hvc1_tag" == "hvc1" ]]
  hvc1_codec=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 "$hvc1_output")
  [[ "$hvc1_codec" == "hevc" ]]
  hvc1_audio_codec=$(ffprobe -v error -select_streams a:0 -show_entries stream=codec_name -of csv=p=0 "$hvc1_output")
  [[ "$hvc1_audio_codec" == "aac" ]]
  ffmpeg -hide_banner -v error -i "$hvc1_output" -map 0:v:0 -f null -
  hvc1_video_duration=$(ffprobe -v error -select_streams v:0 -show_entries stream=duration -of csv=p=0 "$hvc1_output")
  hvc1_audio_duration=$(ffprobe -v error -select_streams a:0 -show_entries stream=duration -of csv=p=0 "$hvc1_output")
  awk -v video="$hvc1_video_duration" -v audio="$hvc1_audio_duration" 'BEGIN { difference=video-audio; if (difference < 0) difference=-difference; if (difference > 0.1) exit 1 }'
  hevc_duration=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$hevc_output")
  hvc1_duration=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$hvc1_output")
  awk -v before="$hevc_duration" -v after="$hvc1_duration" 'BEGIN { difference=before-after; if (difference < 0) difference=-difference; if (difference > 0.1) exit 1 }'
  hevc_video_packets=$(ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts,size,flags -of csv=p=0 "$hevc_output")
  hvc1_video_packets=$(ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts,size,flags -of csv=p=0 "$hvc1_output")
  [[ "$hevc_video_packets" == "$hvc1_video_packets" ]]
  ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts -of csv=p=0 "$hvc1_output" |
    awk -F, 'BEGIN { have_dts=0 } $2 != "N/A" { if (have_dts && $2 < last_dts) exit 1; last_dts=$2; have_dts=1 }'
  echo "HEVC VAAPI integration passed: hev1 and hvc1 tags"
fi
