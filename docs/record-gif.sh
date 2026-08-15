#!/usr/bin/env bash
set -euo pipefail

record_repo_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
record_output=${1:-"$record_repo_dir/docs/temo.gif"}

for record_tool in go convert montage ffmpeg; do
  if ! command -v "$record_tool" >/dev/null 2>&1; then
    printf 'record-gif: required command not found: %s\n' "$record_tool" >&2
    exit 1
  fi
done

record_tmp=$(mktemp -d)
trap 'find "$record_tmp" -depth -delete' EXIT
record_src="$record_tmp/src"
record_frames="$record_tmp/frames"
record_tiles="$record_tmp/tiles"
record_grids="$record_tmp/grids"
mkdir "$record_src" "$record_tiles" "$record_grids"

cp "$record_repo_dir"/*.go "$record_repo_dir/go.mod" "$record_repo_dir/go.sum" "$record_src/"
cp "$record_repo_dir/docs/record-gif_test.go" "$record_src/gif_record_test.go"

printf 'Rendering TEMO frames...\n'
(
  cd "$record_src"
  TEMO_GIF_FRAMES_DIR="$record_frames" \
    go test -tags gifrecord -run '^TestRecordTiledGIFFrames$' -count=1
)

record_names=("PLASMA" "TUNNEL" "METABALLS" "TORUS KNOT" "ROTOZOOM" "STARFIELD" "INFERNO" "YOUR COLOR")
record_colors=("#FF5FD2" "#00D8FF" "#A3FF12" "#FF9F1C" "#9D4EDD" "#3A86FF" "#FF3355" "#FFD60A")

printf 'Composing eight colored tiles...\n'
for record_frame in 0 1 2 3 4 5 6 7 8 9 10 11; do
  record_tile_paths=()
  for record_tile in 1 2 3 4 5 6 7 8; do
    record_name=${record_names[$((record_tile - 1))]}
    record_color=${record_colors[$((record_tile - 1))]}
    record_input=$(printf '%s/tile-%d-frame-%02d.png' "$record_frames" "$record_tile" "$record_frame")
    record_tile_output=$(printf '%s/tile-%d-frame-%02d.png' "$record_tiles" "$record_tile" "$record_frame")

    convert "$record_input" \
      -bordercolor '#070B0E' -border 3x3 \
      -background '#070B0E' -gravity south -splice 0x18 \
      -font DejaVu-Sans-Mono-Bold -pointsize 8 \
      -fill "$record_color" -gravity southwest -annotate +5+5 "◆ TEMO $record_tile/8" \
      -gravity southeast -annotate +5+5 "$record_name" \
      "$record_tile_output"
    record_tile_paths+=("$record_tile_output")
  done

  record_grid=$(printf '%s/frame-%02d.png' "$record_grids" "$record_frame")
  montage "${record_tile_paths[@]}" \
    -tile 4x2 -geometry +2+2 -background '#05080A' "$record_grid"
done

mkdir -p -- "$(dirname -- "$record_output")"
printf 'Encoding %s...\n' "$record_output"
ffmpeg -hide_banner -loglevel error -y \
  -framerate 8 \
  -i "$record_grids/frame-%02d.png" \
  -filter_complex \
    '[0:v]split[palette_source][video];[palette_source]palettegen=max_colors=256:stats_mode=full[palette];[video][palette]paletteuse=dither=bayer:bayer_scale=3:diff_mode=rectangle' \
  -loop 0 \
  "$record_output"

printf 'Wrote %s\n' "$record_output"
