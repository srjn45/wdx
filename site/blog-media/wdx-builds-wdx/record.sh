#!/bin/sh
# Records this post's videos against a throwaway demo (../demo/setup.sh), with
# real Claude Code agents: children.tape, then plan.tape in the same demo, so
# that the folder Claude Code was told to trust in the first is trusted in the
# second. The demo is removed at the end, whatever happens.
#
#   site/blog-media/wdx-builds-wdx/record.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
eval "$("$here/../demo/setup.sh" up)"
trap '"$here/../demo/setup.sh" down' EXIT
cp "$here/extras.yaml" "$DEMO/notes/extras.yaml"
echo extras.yaml >>"$DEMO/notes/.git/info/exclude"

cd "$here"
# VHS's Chromium runs headless: without a sandbox, which this kernel does not
# allow it, and away from the desktop's display.
for tape in children.tape plan.tape; do
  env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-vhs}" "$tape"
done

# A WebM beside each MP4, for the browsers without H.264.
for video in ../../public/blog/wdx-builds-wdx/*.mp4; do
  ffmpeg -loglevel error -y -i "$video" -c:v libvpx-vp9 -b:v 0 -crf 40 -row-mt 1 -an "${video%.mp4}.webm"
done
