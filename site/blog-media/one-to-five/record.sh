#!/bin/sh
# Records this post's videos against a throwaway demo (../demo/setup.sh), with
# real Claude Code agents and a Codex agent:
#   five.tape: parent notes-cli with 3 children delete/search/tests, Codex docs,
#              answering tests in its tab, and the stats tab.
#   restore.tape: stand-in reboot (stop daemon, kill holders), agent shows lost,
#                 wdx agent start brings it back.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
eval "$("$here/../demo/setup.sh" up)"
trap '"$here/../demo/setup.sh" down' EXIT

# Helper script for simulated reboot: stops daemon and kills its holders only
cat >"$DEMO/reboot.sh" <<'REBOOT'
#!/bin/sh
wdx daemon stop >/dev/null 2>&1 || true
pids=$(grep -lzx "WDX_HOME=$WDX_HOME" /proc/[0-9]*/environ 2>/dev/null | cut -d/ -f3 || true)
for p in $pids; do
  if [ -f "/proc/$p/cmdline" ] && grep -qa "holder" "/proc/$p/cmdline"; then
    kill -9 "$p" 2>/dev/null || true
  fi
done
REBOOT
chmod +x "$DEMO/reboot.sh"

# Seed usage in demo wdx.db with real percentages
sqlite3 "$WDX_HOME/wdx.db" <<'SQL'
INSERT OR REPLACE INTO aca_usage VALUES('claude_code','seven_day','',98,strftime('%s','now')*1000 + 8*3600*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('claude_code','five_hour','',1,strftime('%s','now')*1000 + 4*3600*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('codex','10080m','',16,strftime('%s','now')*1000 + 24*3600*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('cursor','month','',3,strftime('%s','now')*1000 + 19*86400*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('antigravity','weekly','gemini-argon-wtu',100,strftime('%s','now')*1000 + 5*86400*1000,1,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('antigravity','weekly','gemini',4,strftime('%s','now')*1000 + 2*86400*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('antigravity','5h','gemini',3,strftime('%s','now')*1000 + 3*3600*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('antigravity','weekly','3p',0,strftime('%s','now')*1000 + 2*86400*1000,0,strftime('%s','now')*1000,NULL);
INSERT OR REPLACE INTO aca_usage VALUES('antigravity','5h','3p',0,strftime('%s','now')*1000 + 4*3600*1000,0,strftime('%s','now')*1000,NULL);
SQL

# Trust demo root and worktrees delete/search in claude config, but leave tests untrusted
claude_json="$HOME/.claude.json"
if [ -f "$claude_json" ]; then
  cp "$claude_json" "$claude_json.demo-bak"
  python3 -c "
import json
p = '$claude_json'
d = json.load(open(p))
projects = d.setdefault('projects', {})
for sub in ['$DEMO/notes', '$DEMO/notes/.worktrees/notes-delete', '$DEMO/notes/.worktrees/notes-search']:
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

cd "$DEMO/notes"
echo "Creating agents..."
wdx agent create --name notes-cli --aca claude_code --prompt "You are the lead agent for notes."
sleep 1
wdx agent create --parent-name notes-cli --name delete --worktree notes-delete --aca claude_code --prompt "Add delete command"
sleep 1
wdx agent create --parent-name notes-cli --name search --worktree notes-search --aca claude_code --prompt "Add search command"
sleep 1
wdx agent create --parent-name notes-cli --name tests --worktree notes-tests --aca claude_code --permissions ask --prompt "Add test_notes.py"
sleep 1
wdx agent create --name docs --aca codex --prompt "Document notes.py in README.md"
sleep 4

cd "$here"
mkdir -p ../../public/blog/one-to-five
echo "Recording five.tape..."
env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" five.tape

echo "Recording restore.tape..."
env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" restore.tape

# Smaller MP4s and WebMs under 3MB total
for video in ../../public/blog/one-to-five/*.mp4; do
  ffmpeg -loglevel error -y -i "$video" -c:v libx264 -crf 32 -preset veryslow -pix_fmt yuv420p -an -movflags +faststart "$video.tmp.mp4"
  mv "$video.tmp.mp4" "$video"
  ffmpeg -loglevel error -y -i "$video" -c:v libvpx-vp9 -b:v 0 -crf 50 -row-mt 1 -an "${video%.mp4}.webm"
done
