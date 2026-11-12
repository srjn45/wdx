#!/bin/sh
# Records this post's lifecycle video against a throwaway demo (../demo/setup.sh):
#   tide-tour.tape: Parent coordinator and child count agent, TIDE project pane navigation,
#                   in-tab diff review with inline comments (c), batch review send (s),
#                   agent feedback resolution, one-key landing (L), worktree removal,
#                   and child termination.
#
#   site/blog-media/tide-tour/record.sh
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
for sub in ['$DEMO/notes', '$DEMO/notes/.worktrees/notes-count']:
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
# Configure landing so it doesn't prompt for first-time setup in TIDE
wdx project landing --name notes --no-check --method rebase

echo "Creating parent coordinator agent..."
wdx agent create --name coordinator --aca claude_code --prompt "You are the project coordinator."
sleep 1

echo "Creating child count agent in worktree..."
wdx agent create --parent-name coordinator --name count --worktree notes-count --aca claude_code --prompt "Add count command to notes.py"
sleep 1

wt="$DEMO/notes/.worktrees/notes-count"
git -C "$wt" config user.name demo
git -C "$wt" config user.email demo@example.com

cat <<'PY' > "$wt/notes.py"
#!/usr/bin/env python3
"""notes: a tiny command-line notebook, one note per line in notes.txt."""
import sys
from pathlib import Path

NOTES = Path(__file__).with_name("notes.txt")


def load():
    return NOTES.read_text().splitlines() if NOTES.exists() else []


def add(text):
    with NOTES.open("a") as f:
        f.write(text + "\n")
    print(f"added note {len(load())}")


def show():
    for i, note in enumerate(load(), 1):
        print(f"{i:3}  {note}")


def count():
    print(f"Total notes: {len(load())}")


def main(argv):
    if len(argv) >= 2 and argv[0] == "add":
        add(" ".join(argv[1:]))
    elif argv in ([], ["list"]):
        show()
    elif argv in (["count"], ["total"]):
        count()
    else:
        print("usage: notes.py [list | add <text> | count]", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
PY

git -C "$wt" commit -am "feat: add count command to notes.py"

cd "$here"
mkdir -p ../../public/blog/tide-tour

echo "Recording tide-tour.tape..."
export DEMO WDX_HOME
env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" tide-tour.tape

# Compress MP4 and generate WebM under 3MB total
echo "Optimising videos..."
for video in ../../public/blog/tide-tour/*.mp4; do
  ffmpeg -loglevel error -y -i "$video" -c:v libx264 -crf 32 -preset veryslow -pix_fmt yuv420p -an -movflags +faststart "$video.tmp.mp4"
  mv "$video.tmp.mp4" "$video"
  ffmpeg -loglevel error -y -i "$video" -c:v libvpx-vp9 -b:v 0 -crf 50 -row-mt 1 -an "${video%.mp4}.webm"
done

echo "Media generation complete!"
