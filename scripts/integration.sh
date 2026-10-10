#!/usr/bin/env bash
set -euo pipefail

assert_duration_delta() {
	local a="$1"
	local b="$2"
	local max_delta="${3:-0.1}"
	awk -v a="$a" -v b="$b" -v max="$max_delta" 'BEGIN {
		diff = a - b;
		if (diff < 0) diff = -diff;
		if (diff > max) exit 1;
	}'
}

probe_stream() {
	local stream="$1"
	local entry="$2"
	local file="$3"
	ffprobe -v error -select_streams "$stream" -show_entries "stream=$entry" -of csv=p=0 "$file"
}

probe_format() {
	local entry="$1"
	local file="$2"
	ffprobe -v error -show_entries "format=$entry" -of csv=p=0 "$file"
}

assert_av_sync() {
	local file="$1"
	local max_delta="${2:-0.1}"
	local v_dur
	local a_dur
	v_dur=$(probe_stream v:0 duration "$file")
	a_dur=$(probe_stream a:0 duration "$file")
	assert_duration_delta "$v_dur" "$a_dur" "$max_delta"
}

verify_media() {
	local file="$1"
	[[ -s "$file" ]]
	ffprobe -hide_banner -v error -show_streams -show_format "$file" >/dev/null
	ffmpeg -hide_banner -v error -i "$file" -f null -
}

assert_monotonic_timestamps() {
	local file="$1"
	ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts -of csv=p=0 "$file" |
		awk -F, 'BEGIN { last_pts=-1; last_dts=-1 }
			$1 != "N/A" && $1 < last_pts { exit 1 }
			$2 != "N/A" && $2 < last_dts { exit 1 }
			{ if ($1 != "N/A") last_pts=$1; if ($2 != "N/A") last_dts=$2 }'
}

assert_finite_audio() {
	local file="$1"
	local log="$work_dir/audio.log"
	ffmpeg -hide_banner -v info -i "$file" -map 0:a:0 -af astats=metadata=1:reset=0 -f null - 2>"$log"
	awk -F': ' '/Number of NaNs:/ || /Number of Infs:/ { if (($2 + 0) != 0) exit 1 }' "$log"
}

binary=${1:-./build/lecturejet}
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

"$(dirname "$0")/generate_synthetic.sh" "$work_dir"
input="$work_dir/synthetic-mono-48k.mp4"
stereo_input="$work_dir/synthetic-stereo-44k.mp4"

# Validate synthetic inputs
[[ $(probe_stream a:0 sample_rate "$stereo_input") == "44100" ]]
[[ $(probe_stream a:0 channels "$stereo_input") == "2" ]]

# Reject audio-only input
audio_only="$work_dir/audio-only.mp4"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'sine=frequency=440:duration=1' -c:a aac "$audio_only"
audio_only_err=$("$binary" "$audio_only" "$work_dir/audio-out.mp4" --non-interactive 2>&1 || true)
grep -q 'input has no video stream' <<<"$audio_only_err"

# Locate VAAPI render device
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

# H.264 VAAPI render
output="$work_dir/output-h264.mp4"
"$binary" "$input" "$output" --non-interactive --vaapi-device "$device" \
	--video-codec h264 --mono --threads 2
verify_media "$output"
assert_finite_audio "$output"
assert_av_sync "$output"
assert_monotonic_timestamps "$output"
echo "H.264 VAAPI integration passed: A/V duration delta <= 100ms"

# Stereo VAAPI render
stereo_output="$work_dir/output-stereo.mp4"
"$binary" "$stereo_input" "$stereo_output" --non-interactive --vaapi-device "$device" \
	--video-codec h264 --stereo --threads 2
verify_media "$stereo_output"
[[ $(probe_stream a:0 channels "$stereo_output") == "2" ]]
[[ $(probe_stream a:0 sample_rate "$stereo_output") == "48000" ]]
assert_av_sync "$stereo_output"
echo "Stereo VAAPI integration passed: channels=2, sample_rate=48000"

# Probe HEVC VAAPI capability
input_width=$(probe_stream v:0 width "$input")
input_height=$(probe_stream v:0 height "$input")
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
	verify_media "$hevc_output"
	[[ $(probe_stream v:0 codec_tag_string "$hevc_output") == "hev1" ]]

	hvc1_output="$work_dir/output-hevc-hvc1.mp4"
	"$binary" "$input" "$hvc1_output" --non-interactive --vaapi-device "$device" \
		--video-codec hevc --hevc-tag hvc1 --mono --threads 2
	verify_media "$hvc1_output"
	[[ $(probe_stream v:0 codec_tag_string "$hvc1_output") == "hvc1" ]]
	[[ $(probe_stream v:0 codec_name "$hvc1_output") == "hevc" ]]
	[[ $(probe_stream a:0 codec_name "$hvc1_output") == "aac" ]]
	assert_av_sync "$hvc1_output"
	assert_duration_delta "$(probe_format duration "$hevc_output")" "$(probe_format duration "$hvc1_output")" 0.1

	hevc_packets=$(ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts,flags -of csv=p=0 "$hevc_output")
	hvc1_packets=$(ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts,flags -of csv=p=0 "$hvc1_output")
	[[ "$hevc_packets" == "$hvc1_packets" ]]
	assert_monotonic_timestamps "$hvc1_output"

	echo "HEVC VAAPI integration passed: hev1 and hvc1 tags"
fi

# VAAPI hardware scale / format conversion test (10-bit input to 8-bit NV12)
hdr10_input="$work_dir/synthetic-10bit.mp4"
ffmpeg -hide_banner -loglevel error -y \
	-f lavfi -i 'testsrc2=size=320x240:rate=30:duration=4' \
	-f lavfi -i 'sine=frequency=440:sample_rate=48000:duration=4' \
	-t 4 -c:v libx264 -pix_fmt yuv420p10le -c:a aac -shortest "$hdr10_input"
hdr10_output="$work_dir/output-10bit.mp4"
"$binary" "$hdr10_input" "$hdr10_output" --non-interactive --vaapi-device "$device" \
	--video-codec h264 --mono --threads 2
verify_media "$hdr10_output"
assert_av_sync "$hdr10_output"
assert_monotonic_timestamps "$hdr10_output"
echo "VAAPI scaling integration passed: 10-bit conversion"

