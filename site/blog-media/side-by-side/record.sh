#!/bin/sh
# Records this post's videos against a throwaway demo (../demo/setup.sh), with
# a real Claude Code and a real Codex: side-by-side.tape, then land.tape in the
# same demo, which lands what the first left. The demo's repository first gets
# test_notes.py, the check its landing runs. The demo is removed at the end,
# whatever happens.
#
#   site/blog-media/side-by-side/record.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
eval "$("$here/../demo/setup.sh" up)"
trap '"$here/../demo/setup.sh" down' EXIT
cp "$here/test_notes.py" "$DEMO/notes/"
git -C "$DEMO/notes" add test_notes.py
git -C "$DEMO/notes" commit -q -m "notes: test add and list"

cd "$here"
mkdir -p ../../public/blog/side-by-side
# VHS's Chromium runs headless: without a sandbox, which this kernel does not
# allow it, and away from the desktop's display.
for tape in side-by-side.tape land.tape; do
  env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-vhs}" "$tape"
done

# Smaller MP4s, to keep the post's media under 3 MB, and a WebM beside each,
# for the browsers without H.264.
for video in ../../public/blog/side-by-side/*.mp4; do
  ffmpeg -loglevel error -y -i "$video" -c:v libx264 -crf 32 -preset veryslow -pix_fmt yuv420p -an -movflags +faststart "$video.tmp.mp4"
  mv "$video.tmp.mp4" "$video"
  ffmpeg -loglevel error -y -i "$video" -c:v libvpx-vp9 -b:v 0 -crf 50 -row-mt 1 -an "${video%.mp4}.webm"
done
