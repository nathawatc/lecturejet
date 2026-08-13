#!/usr/bin/env bash
set -euo pipefail

output_dir=${1:-testdata}
mkdir -p "$output_dir"

ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i 'testsrc2=size=320x240:rate=30:duration=14.5' \
  -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=5[s0];anullsrc=r=48000:cl=mono:d=2[s1];sine=frequency=660:sample_rate=48000:duration=4[s2];anullsrc=r=48000:cl=mono:d=0.5[s3];sine=frequency=880:sample_rate=48000:duration=3[s4];[s0][s1][s2][s3][s4]concat=n=5:v=0:a=1" \
  -t 14.5 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest \
  "$output_dir/synthetic-mono-48k.mp4"

ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i 'testsrc2=size=320x240:rate=30000/1001:duration=8' \
  -f lavfi -i "sine=frequency=520:sample_rate=44100:duration=3[s0];anullsrc=r=44100:cl=stereo:d=1.2[s1];sine=frequency=720:sample_rate=44100:duration=3[s2];[s0][s1][s2]concat=n=3:v=0:a=1" \
  -t 7.2 -c:v libx264 -pix_fmt yuv420p -c:a aac -ar 44100 -ac 2 -shortest \
  "$output_dir/synthetic-stereo-44k.mp4"
