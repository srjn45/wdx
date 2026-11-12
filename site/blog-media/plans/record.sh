#!/bin/sh
# Records this post's videos against a throwaway demo (../demo/setup.sh), with
# real Claude Code agents:
#   plan-run.tape: runs release plan (contributing -> license), hands off summaries,
#                  and lands on plan/release.
#   plan-retry.tape: inspects land failed (landing check failure), fixes in slot,
#                    and lands with wdx plan task land.
#
#   site/blog-media/plans/record.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
eval "$("$here/../demo/setup.sh" up)"
trap '"$here/../demo/setup.sh" down' EXIT

claude_json="$HOME/.claude.json"
if [ -f "$claude_json" ]; then
  cp "$claude_json" "$claude_json.demo-bak"
  python3 -c "
import json
p = '$claude_json'
d = json.load(open(p))
projects = d.setdefault('projects', {})
for sub in ['$DEMO/notes', '$DEMO/notes/.worktrees/release-1', '$DEMO/notes/.worktrees/release-2', '$DEMO/notes/.worktrees/fail-test-1']:
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

# Prepare release.yaml for plan-run.tape
cat <<'YAML' >"$DEMO/notes/release.yaml"
goal: Get notes ready for open-source contributors.
constraints:
  - Keep files concise.
tasks:
  - id: contributing
    aca: claude_code
    model: sonnet
    prompt: |
      Create CONTRIBUTING.md explaining how to run notes.py, commit it, then say done with plan task done.
  - id: license
    aca: claude_code
    model: sonnet
    after: [contributing]
    prompt: |
      Create LICENSE with MIT text, commit it, then say done with plan task done.
YAML

cd "$here"
mkdir -p ../../public/blog/plans

echo "Recording plan-run.tape..."
env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" plan-run.tape

# Setup failure state for plan-retry.tape
echo "Setting up fail-test plan..."
cd "$DEMO/notes"
wdx project landing --name notes --check 'python3 -c "assert \"FAIL\" not in open(\"notes.txt\").read()"'

cat <<'YAML' >fail-plan.yaml
goal: Test failure handling.
tasks:
  - id: add-fail
    aca: claude_code
    model: sonnet
    prompt: Append 'FAIL' to notes.txt, commit it, and say done.
YAML

wdx plan create --name fail-test --path fail-plan.yaml
wdx plan run --name fail-test

for i in $(seq 1 30); do
  status=$(wdx plan status --name fail-test 2>/dev/null || true)
  if echo "$status" | grep -q "land failed"; then
    echo "Fail-test reached land failed status."
    break
  fi
  sleep 2
done

cd "$here"
echo "Recording plan-retry.tape..."
env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" plan-retry.tape

# Compress MP4s and generate WebMs under 3MB total
echo "Optimising videos..."
for video in ../../public/blog/plans/*.mp4; do
  ffmpeg -loglevel error -y -i "$video" -c:v libx264 -crf 32 -preset veryslow -pix_fmt yuv420p -an -movflags +faststart "$video.tmp.mp4"
  mv "$video.tmp.mp4" "$video"
  ffmpeg -loglevel error -y -i "$video" -c:v libvpx-vp9 -b:v 0 -crf 50 -row-mt 1 -an "${video%.mp4}.webm"
done

echo "Media generation complete!"
