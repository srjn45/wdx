"""Records what the wdx doc CODEX_BACKUP reads: Codex's session files of one
conversation, in full mode. The session runs a shell command, changes a file, uses
a subagent and is compacted; Codex is quit and the session resumed with
`codex resume <id>` for a second turn; then /new and one turn in the new session.

    python3 transcript.py <name>

Besides the screens, sessions/ keeps every session file of the work directory as
Codex left it, its subagent's included, and first-run/ the same files when Codex
was first quit, before the resume, to see how a resume writes them.
"""
import glob, json, os, shutil, sys, time
from rec import Rec

FULL = '--dangerously-bypass-approvals-and-sandbox'
SESSIONS = os.path.expanduser('~/.codex/sessions')

name = sys.argv[1]
r = Rec(name, 120, 40, FULL)
began = time.time()


def ready():
    r.wait(r'^› (?:.*\n)+?\s{2}\S.* · ', 90)
    r.still(2)


def idle(limit=240):
    """Waits for a turn to end: working shown, then gone."""
    r.wait(r'esc to interrupt', 60)
    end = time.time() + limit
    while time.time() < end and 'esc to interrupt' in r.screen():
        time.sleep(0.2)
    r.still(2)


def turn(snap, text):
    """As wdx delivers a message: a paste, a moment, then Tab."""
    r.paste(text)
    time.sleep(0.15)
    r.keys('Tab')
    idle()
    r.snap(snap)


def command(text):
    r.type(text)
    time.sleep(0.5)
    r.keys('Enter')


def files():
    """The session files Codex wrote for the work directory since this run began,
    and those of their subagents."""
    found, ids = {}, set()
    for path in sorted(glob.glob(f'{SESSIONS}/*/*/*/rollout-*.jsonl')):
        if os.path.getmtime(path) < began - 1:
            continue
        with open(path) as f:
            meta = json.loads(f.readline())['payload']
        found[path] = meta
    # A subagent may work elsewhere; it names the thread it belongs to.
    for path, meta in found.items():
        if meta.get('cwd') == r.work:
            ids.add(meta['id'])
    return [p for p, m in found.items() if m.get('cwd') == r.work or m.get('parent_thread_id') in ids]


def keep(into):
    os.makedirs(f'{r.out}/{into}', exist_ok=True)
    for path in files():
        shutil.copy(path, f'{r.out}/{into}/{os.path.basename(path)}')


ready()
r.snap('started')
turn('command', 'Run the shell command `echo hello`, then reply with the word DONE.')
turn('file_change', 'With apply_patch, create the file notes.txt holding the line `one`, then reply with the word DONE.')
turn('subagent', 'Use a subagent to run the shell command `echo sub` and report what it printed, then reply with the word DONE.')
command('/compact')
idle()
r.snap('compacted')
# The session id is that of the work directory's only user thread so far.
first = [p for p in files() if json.loads(open(p).readline())['payload'].get('thread_source') == 'user']
assert len(first) == 1, first
session = json.loads(open(first[0]).readline())['payload']['id']
r.quit()
keep('first-run')

r.start(f'{FULL} resume {session}', raw='resume')
ready()
r.snap('resumed')
turn('resumed_turn', 'Reply with the word AGAIN.')
command('/new')
# In a git repository, /new first asks where the new conversation runs.
time.sleep(2)
r.snap('new_asks')
r.type('1')
ready()
r.snap('new')
turn('new_turn', 'Run the shell command `echo new`, then reply with the word DONE.')
time.sleep(1)
r.end()
keep('sessions')
