#!/bin/sh
# Renders cover.svg to the post's cover, 1200x630, with the site's fonts, in
# the headless Chromium VHS downloads (CHROME names another), cut to size by
# ffmpeg.
#   site/blog-media/side-by-side/render.sh   after npm ci in site/
set -eu
here=$(cd "$(dirname "$0")" && pwd)
site=$here/../..
fonts=$site/node_modules/@fontsource
out=$site/src/assets/blog/side-by-side/cover.png
chrome=${CHROME:-$(ls -d "$HOME"/.cache/rod/browser/chromium-*/chrome | tail -1)}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cat >"$tmp/cover.html" <<HTML
<!doctype html><meta charset="utf-8">
<style>
  @font-face { font-family: "Bricolage Grotesque"; font-weight: 800; src: url("file://$fonts/bricolage-grotesque/files/bricolage-grotesque-latin-800-normal.woff2"); }
  @font-face { font-family: "JetBrains Mono"; font-weight: 400; src: url("file://$fonts/jetbrains-mono/files/jetbrains-mono-latin-400-normal.woff2"); }
  @font-face { font-family: "JetBrains Mono"; font-weight: 700; src: url("file://$fonts/jetbrains-mono/files/jetbrains-mono-latin-700-normal.woff2"); }
  html, body { margin: 0; background: #13161c; }
  svg { display: block; }
</style>
$(cat "$here/cover.svg")
HTML
mkdir -p "$(dirname "$out")"
env -u DISPLAY -u WAYLAND_DISPLAY "$chrome" --headless --no-sandbox --disable-gpu --hide-scrollbars \
  --window-size=1200,800 --virtual-time-budget=3000 --screenshot="$tmp/shot.png" "file://$tmp/cover.html" 2>/dev/null
# The window has room for the browser's own bar: cut the page out of it.
ffmpeg -loglevel error -y -i "$tmp/shot.png" -vf crop=1200:630:0:0 "$out"
echo "$out"
