#!/bin/sh
# Records this post's videos against a throwaway demo (../demo/setup.sh):
#   1. lost-terminals.tape: daemon holder detachment, simulated reboot, state: lost, and session recovery via wdx agent start.
#   2. clashing-edits.tape: isolated git worktrees (--worktree) preventing branch collisions and keeping main clean.
#   3. unanswered-prompts.tape: VT screen parser detecting prompt, amber row and '?' badge in TIDE, instant in-place response.
#   4. mystery-diffs.tape: in-tab diff review (o), guarded one-key landing (L) with automated test checks and worktree cleanup.
#
#   site/blog-media/four-failures/record.sh [all | 1 | 2 | 3 | 4]
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
for sub in ['$DEMO/notes', '$DEMO/notes/.worktrees/notes-worker', '$DEMO/notes/.worktrees/notes-delete', '$DEMO/notes/.worktrees/notes-search', '$DEMO/notes/.worktrees/notes-count']:
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

# Helper script for simulated reboot: stops daemon and kills its holders
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

cd "$DEMO/notes"
wdx project landing --name notes --no-check --method rebase

mkdir -p "$here/../../public/blog/four-failures"
STEP="${1:-all}"

# ==========================================
# 1. Recording lost-terminals.tape
# ==========================================
if [ "$STEP" = "all" ] || [ "$STEP" = "1" ]; then
  echo "=== Step 1: Recording lost-terminals.tape ==="
  wdx agent create --name notes-worker --aca claude_code --prompt "You are the maintainer of notes."
  sleep 2

  cd "$here"
  export DEMO WDX_HOME
  env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" lost-terminals.tape

  # Clean up notes-worker
  cd "$DEMO/notes"
  wdx agent remove --stop-running --name notes-worker >/dev/null 2>&1 || true
fi

# ==========================================
# 2. Recording clashing-edits.tape
# ==========================================
if [ "$STEP" = "all" ] || [ "$STEP" = "2" ]; then
  echo "=== Step 2: Recording clashing-edits.tape ==="
  cd "$DEMO/notes"
  wdx agent create --name notes-delete --worktree notes-delete --aca claude_code --prompt "Add delete command"
  sleep 1
  wdx agent create --name notes-search --worktree notes-search --aca codex --prompt "Add search command"
  sleep 1

  # Add focused changes to each worktree
  wt_del="$DEMO/notes/.worktrees/notes-delete"
  git -C "$wt_del" config user.name demo
  git -C "$wt_del" config user.email demo@example.com
  cat <<'PY' >> "$wt_del/notes.py"

def delete(index):
    lines = load()
    if 1 <= index <= len(lines):
        removed = lines.pop(index - 1)
        NOTES.write_text("\n".join(lines) + "\n" if lines else "")
        print(f"deleted note {index}: {removed}")
PY
  git -C "$wt_del" commit -am "feat: add delete command to notes.py"

  wt_search="$DEMO/notes/.worktrees/notes-search"
  git -C "$wt_search" config user.name demo
  git -C "$wt_search" config user.email demo@example.com
  cat <<'PY' >> "$wt_search/notes.py"

def search(query):
    for i, note in enumerate(load(), 1):
        if query.lower() in note.lower():
            print(f"{i:3}  {note}")
PY
  git -C "$wt_search" commit -am "feat: add search command to notes.py"

  cd "$here"
  env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" clashing-edits.tape
fi

# ==========================================
# 3. Recording unanswered-prompts.tape
# ==========================================
if [ "$STEP" = "all" ] || [ "$STEP" = "3" ]; then
  echo "=== Step 3: Recording unanswered-prompts.tape ==="
  cd "$DEMO/notes"
  # Stop notes-delete to keep the agent list clean
  wdx agent remove --stop-running --name notes-delete >/dev/null 2>&1 || true

  # Create notes-tests in an untrusted folder with permissions ask
  wdx agent create --name notes-tests --worktree notes-tests --aca claude_code --permissions ask --prompt "Add test_notes.py"
  sleep 4

  cd "$here"
  env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" unanswered-prompts.tape

  # Clean up
  cd "$DEMO/notes"
  wdx agent remove --stop-running --name notes-tests >/dev/null 2>&1 || true
  wdx agent remove --stop-running --name notes-search >/dev/null 2>&1 || true
fi

# ==========================================
# 4. Recording mystery-diffs.tape
# ==========================================
if [ "$STEP" = "all" ] || [ "$STEP" = "4" ]; then
  echo "=== Step 4: Recording mystery-diffs.tape ==="
  cd "$DEMO/notes"
  wdx agent create --name coordinator --aca claude_code --prompt "You are the project coordinator."
  sleep 1

  git -C "$DEMO/notes" worktree add -b notes-count .worktrees/notes-count main
  wt_count="$DEMO/notes/.worktrees/notes-count"
  git -C "$wt_count" config user.name demo
  git -C "$wt_count" config user.email demo@example.com
  cat <<'PY' >> "$wt_count/notes.py"

def count():
    print(f"Total notes: {len(load())}")
PY
  git -C "$wt_count" commit -am "feat: add count command to notes.py"

  cd "$here"
  env -u DISPLAY -u WAYLAND_DISPLAY VHS_NO_SANDBOX=true "${VHS:-/home/srjn45/go/bin/vhs}" mystery-diffs.tape
fi

# ==========================================
# 5. Optimise MP4s and generate WebMs
# ==========================================
echo "=== Step 5: Optimising videos ==="
for video in "$here/../../public/blog/four-failures"/*.mp4; do
  [ -f "$video" ] || continue
  ffmpeg -loglevel error -y -i "$video" -c:v libx264 -crf 32 -preset veryslow -pix_fmt yuv420p -an -movflags +faststart "$video.tmp.mp4"
  mv "$video.tmp.mp4" "$video"
  ffmpeg -loglevel error -y -i "$video" -c:v libvpx-vp9 -b:v 0 -crf 50 -row-mt 1 -an "${video%.mp4}.webm"
done

echo "=== Media processing complete! ==="
