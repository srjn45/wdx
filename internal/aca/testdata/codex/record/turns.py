"""Records what the wdx doc CODEX_ACTIVITY reads: turns of each kind, in full mode,
or an approval, in ask mode. frames.log keeps every bottom of the screen that
changed, to check the marks against.

    python3 turns.py <name> <cols> <rows> [full|ask]
"""
import sys, threading, time
from rec import Rec

name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
mode = sys.argv[4] if len(sys.argv) > 4 else 'full'
r = Rec(name, cols, rows, '--dangerously-bypass-approvals-and-sandbox' if mode == 'full' else '')
stop = threading.Event()
# After a mark, before the next key, so a mark a little off still reads its step's screen.
STEP = 1.5
snap = r.snap


def marked(name):
    snap(name)
    time.sleep(STEP)


r.snap = marked


def frames():
    prev = None
    with open(f'{r.out}/frames.log', 'w') as f:
        while not stop.is_set():
            bottom = '\n'.join([l for l in r.lines() if l.strip()][-10:])
            if bottom != prev:
                f.write(f'--- {time.time() - r.t0:7.2f}\n{bottom}\n')
                f.flush()
                prev = bottom
            time.sleep(0.05)


threading.Thread(target=frames, daemon=True).start()


def send(text):
    """As wdx delivered a message when this was recorded: a paste, a moment, then
    Tab. It presses Enter now."""
    r.paste(text)
    time.sleep(0.15)
    r.keys('Tab')


def step(name, pattern, limit=120):
    """Marks a step once pattern shows at the bottom, after a moment for it to settle."""
    if r.wait(pattern, limit):
        time.sleep(0.4)
        r.snap(name)


def above_box():
    """The first line above the box that is not blank."""
    lines = r.lines()
    box = max([i for i, l in enumerate(lines) if l.startswith('› ')], default=0)
    for l in reversed(lines[:box]):
        if l.strip():
            return l
    return ''


def step_when(name, ok, limit=120):
    end = time.time() + limit
    while time.time() < end:
        if ok(above_box()):
            time.sleep(0.2)
            if ok(above_box()):
                r.snap(name)
                return
        time.sleep(0.02)
    print('TIMEOUT', name, flush=True)


r.wait(r'^› (?:.*\n)+?\s{2}\S.* · ')
r.still(2)
r.snap('started')
if mode == 'ask':
    send('Run the shell command `curl -sI https://example.com` and tell me the status line.')
    step('approval', r'esc to cancel')
    r.keys('Escape')
    step('denied', r'Conversation interrupted')
else:
    send('Run the shell command `sleep 12; echo hi`, then reply with the word DONE.')
    step('working', r'esc to interrupt\)$')
    # A narrow screen cuts the Working line to "· 1 b…".
    step('working_background', r'esc to interrupt\) · 1 b')
    step('tool_done', r'Worked for')

    send('Write a poem of 30 short lines about the sea, with no tools.')
    step_when('streaming', lambda above: not above.startswith(('• Working', '›', '  Worked')) and above.strip() != '')
    step('stream_done', r'Worked for .*\n+› Ask')

    send('Run the shell command `sleep 10; echo hi`, then reply with the word DONE.')
    r.wait(r'esc to interrupt', 60)
    time.sleep(1)
    send('Then reply with the word AGAIN.')
    step('queued', r'Queued follow-up inputs')
    step('queued_done', r'AGAIN(?:.*\n)+?\s+Worked for')

    send('Use a subagent to run the shell command `sleep 15; echo sub` and report what it printed, then reply with the word DONE.')
    step('subagent_waiting', r'Waiting for agents', 90)
    step('subagent_done', r'Worked for .*\n+› Ask', 180)

    send('Run the shell command `sleep 40; echo late` and wait for it to finish, then reply with the word DONE.')
    step('background_in_turn', r'esc to interrupt\) · 1 b', 90)
    r.keys('Escape')
    step('interrupted_background', r'Conversation interrupted.*\n+\s+1 background terminal running')
time.sleep(1)
stop.set()
r.end()
