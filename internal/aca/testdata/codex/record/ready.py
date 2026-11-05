"""Records what docs ACA_READY reads: Codex starting, its box empty, with a draft,
in shell mode, under the / menu, in a turn and after it.

    python3 ready.py <name> <cols> <rows>
"""
import sys, time
from rec import Rec

# Between a step and the next, so a mark a little off still reads the step's screen.
STEP = 1.5

name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
r = Rec(name, cols, rows)
snap = r.snap


def step(name):
    """Marks a step in the middle of a still stretch, as a mark can be a little off."""
    snap(name)
    time.sleep(STEP)


r.snap = step
# The box shows while Codex still loads; the status line under it, once it is ready.
r.wait(r'^› ')
r.snap('loading')
r.wait(r'^› (?:.*\n)+?\s{2}\S.* · ')
time.sleep(STEP)
r.snap('ready')

r.type('hello draft')
time.sleep(STEP)
r.snap('draft')
r.keys('C-c')
time.sleep(STEP)
r.snap('cleared')

r.paste('line one\nline two\nline three')
time.sleep(STEP)
r.snap('multi_line_draft')
r.keys('C-c')
time.sleep(STEP)

r.type('!ls')
time.sleep(STEP)
r.snap('shell_mode')
r.keys('C-c')
time.sleep(STEP)

r.type('/')
time.sleep(STEP)
r.snap('slash_menu')
r.keys('Escape')
time.sleep(STEP)
r.keys('BSpace')
time.sleep(STEP)
r.snap('menu_closed')

# As wdx delivers: a paste, a moment, then Tab.
r.paste('count from 1 to 40, one number per line, then say DONE')
time.sleep(0.15)
r.keys('Tab')
time.sleep(1.5)
r.snap('working')
r.type('typed in turn')
time.sleep(STEP)
r.snap('typed_in_turn')
for _ in range(len('typed in turn')):
    r.keys('BSpace')
r.wait(r'DONE', 120)
r.still()
r.snap('answered')
r.end()
