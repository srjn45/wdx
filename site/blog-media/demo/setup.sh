#!/bin/sh
# Makes a throwaway wdx demo for the blog's recordings: a home of its own in a
# temporary directory, its daemon, and a toy repository opened as a project.
# Nothing of the person's own ~/.wdx, projects, docs or plans is touched.
#
#   eval "$(site/blog-media/demo/setup.sh up)"   makes it, prints the exports
#   site/blog-media/demo/setup.sh down            stops its daemon, removes it
#
# The blog shows a recording in a column about 720px wide: a tape for one
# wants a bigger font than 14 at 1440 wide, about 20, to stay readable.
#
# up prints WDX_HOME and DEMO (the directory holding home/ and notes/) for the
# shell to export; down reads them from the environment.
set -eu
unset WDX_AGENT_ID WDX_TERMINAL_ID WDX_PROJECT

up() {
  DEMO=$(mktemp -d /tmp/wdx-demo.XXXXXX)
  WDX_HOME=$DEMO/home
  export WDX_HOME
  mkdir -p "$WDX_HOME"
  # No metrics leave the demo, and nothing prompts behind a recording's back.
  cat >"$WDX_HOME/config.yaml" <<'YAML'
otel:
  endpoint: ""
YAML

  repo=$DEMO/notes
  mkdir -p "$repo"
  cat >"$repo/notes.py" <<'PY'
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


def main(argv):
    if len(argv) >= 2 and argv[0] == "add":
        add(" ".join(argv[1:]))
    elif argv in ([], ["list"]):
        show()
    else:
        print("usage: notes.py [list | add <text>]", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
PY
  chmod +x "$repo/notes.py"
  printf 'notes.txt\n__pycache__/\n' >"$repo/.gitignore"
  printf '# notes\n\nA tiny command-line notebook: `./notes.py add <text>`, `./notes.py list`.\n' >"$repo/README.md"
  git -C "$repo" init -q -b main
  git -C "$repo" -c user.name=demo -c user.email=demo@example.com add .
  git -C "$repo" -c user.name=demo -c user.email=demo@example.com commit -q -m "notes: add and list"
  git -C "$repo" config user.name demo
  git -C "$repo" config user.email demo@example.com

  wdx daemon start >/dev/null
  wdx project open --local "$repo" >/dev/null

  # The shell may itself run under wdx: its agent's variables would name the
  # wrong home's agent.
  printf 'unset WDX_AGENT_ID WDX_TERMINAL_ID WDX_PROJECT; export DEMO=%s WDX_HOME=%s\n' "$DEMO" "$WDX_HOME"
}

down() {
  case ${DEMO:-} in /tmp/wdx-demo.*) ;; *) echo "setup.sh down: DEMO is not a demo directory" >&2; exit 2 ;; esac
  WDX_HOME=$DEMO/home wdx daemon stop >/dev/null 2>&1 || true
  # The holders, and the shells and AI CLIs in them, outlive the daemon by
  # design: stop every process whose environment names the demo's home.
  pids=$(grep -lzx "WDX_HOME=$DEMO/home" /proc/[0-9]*/environ 2>/dev/null | cut -d/ -f3 || true)
  [ -z "$pids" ] || kill $pids 2>/dev/null || true
  for pid in $pids; do
    n=0
    while kill -0 "$pid" 2>/dev/null; do
      n=$((n + 1))
      [ $n -lt 50 ] || { kill -KILL "$pid" 2>/dev/null || true; break; }
      sleep 0.2
    done
  done
  rm -rf "$DEMO"
}

case ${1:-} in
  up) up ;;
  down) down ;;
  *) echo "usage: setup.sh up | down" >&2; exit 2 ;;
esac
