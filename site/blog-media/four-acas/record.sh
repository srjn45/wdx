#!/bin/sh
# Records this post's video against a throwaway demo (../demo/setup.sh), with
# four real AI coding agents running Claude Code, Codex, Cursor, and Antigravity:
#   four-acas.tape: shows wdx agent acas, wdx usage across all four providers,
#                   wdx agent list with each ACA, and TIDE fleet navigation.
#
#   site/blog-media/four-acas/record.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
eval "$("$here/../demo/setup.sh" up)"

claude_json="$HOME/.claude.json"
if [ -f "$claude_json" ]; then
  cp "$claude_json" "$claude_json.demo-bak"
  python3 -c "
import json
p = '$claude_json'
d = json.load(open(p))
projects = d.setdefault('projects', {})
for sub in ['$DEMO/notes', '$DEMO/notes/.worktrees/notes-search']:
    projects[sub] = {'hasTrustDialogAccepted': True}
json.dump(d, open(p, 'w'), indent=2)
"
fi

restore_claude_json() {
  if [ -f "$HOME/.claude.json.demo-bak" ]; then
    mv -f "$HOME/.claude.json.demo-bak" "$HOME/.claude.json"
  fi
}
trap 'restore_claude_json; "$here/../demo/setup.sh" down' EXIT

# Seed usage in demo wdx.db with clean percentages across all 4 ACAs
sqlite3 "$WDX_HOME/wdx.db" <<'SQL'
INSERT OR REPLACE INTO aca_usage VALUES('claude_code','seven_day','',42,strftime('%s','now')*1000 + 4*86400*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('claude_code','five_hour','',8,strftime('%s','now')*1000 + 3*3600*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('codex','10080m','',16,strftime('%s','now')*1000 + 24*3600*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('cursor','month','',3,strftime('%s','now')*1000 + 19*86400*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('antigravity','weekly','gemini',14,strftime('%s','now')*1000 + 2*86400*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('antigravity','5h','gemini',2,strftime('%s','now')*1000 + 4*3600*1000,0,strftime('%s','now')*1000,NULL);
SQL

cd "$DEMO/notes"
wdx project landing --name notes --no-check --method rebase

echo "Creating agents across all four ACAs..."
wdx agent create --name notes-lead --aca claude_code --prompt "You are the project lead for notes."
sleep 1
wdx agent create --name notes-search --worktree notes-search --aca codex --prompt "Add search command to notes.py"
sleep 1
wdx agent create --name notes-docs --aca cursor --prompt "Document notes CLI usage in README.md"
sleep 1
wdx agent create --name notes-audit --aca antigravity --prompt "Audit notes CLI error handling and input validation"
sleep 3

cd "$here"
mkdir -p ../../public/blog/four-acas

echo "Recording four-acas.tape..."
export DEMO WDX_HOME
env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" four-acas.tape

# Compress MP4 and generate WebM under 3MB total
echo "Optimising videos..."
for video in ../../public/blog/four-acas/*.mp4; do
  ffmpeg -loglevel error -y -i "$video" -c:v libx264 -crf 32 -preset veryslow -pix_fmt yuv420p -an -movflags +faststart "$video.tmp.mp4"
  mv "$video.tmp.mp4" "$video"
  ffmpeg -loglevel error -y -i "$video" -c:v libvpx-vp9 -b:v 0 -crf 50 -row-mt 1 -an "${video%.mp4}.webm"
done

echo "Recording complete!"
