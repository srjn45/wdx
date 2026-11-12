"""A draft at rest and in a turn, for the line above the box (the wdx doc
STATE_LEFTOVERS, section 2): a draft right after startup, one typed while a turn
thinks, one while a tool runs, and each left in the box after its turn.

usage: [CLAUDE_VERSION=2.1.322] restdraft.py <name> <cols> <rows>
"""
import sys, time
from rec import *

# A key right after a snap lands in the marked screen on replay: let it settle first.
_snap = Rec.snap
def snap(self, name):
    _snap(self, name); time.sleep(1.5)
Rec.snap = snap

name = sys.argv[1]; cols = int(sys.argv[2]); rows = int(sys.argv[3])
r = Rec(name, cols=cols, rows=rows,
        claude=os.path.expanduser('~/.local/share/claude/versions/' + os.environ.get('CLAUDE_VERSION', '2.1.322')))
def prompted(): return any('Esc to cancel' in x or x.strip() == 'to cancel' for x in r.nb()[-3:])

if r.wait('Enter to confirm', 30, where=12):
    time.sleep(1.5); r.keys('Down'); time.sleep(.5); r.keys('Enter')
r.wait('❯', 20); time.sleep(3); r.snap('01_idle_start')
r.mark('draft_start'); r.slow('hello there', .1); time.sleep(1); r.snap('02_draft_start')
time.sleep(6); r.snap('03_draft_start_later')
r.keys('C-u'); time.sleep(1.5); r.snap('04_cleared_start')

r.mark('turn_story'); r.say('Write a 300 word story about a lighthouse. No tools.')
time.sleep(.8); r.slow('abc', .1); time.sleep(.3); r.snap('05_draft_in_turn')
r.settle(still=5, limit=60, approve=False); r.mark('draft_after_turn'); r.snap('06_draft_after_turn')
time.sleep(5); r.snap('07_draft_after_turn_later')
r.keys('C-u'); time.sleep(1.5); r.snap('08_cleared_after_turn')

r.mark('turn_tool'); r.say('Run with Bash in the foreground: sleep 8 . Then say done.')
end = time.time() + 30
while time.time() < end and not any('Bash(sleep 8)' in x for x in r.nb()[-12:]):
    if prompted(): time.sleep(1); r.keys('Enter'); time.sleep(1)
    time.sleep(.1)
time.sleep(1.5); r.slow('xyz', .1); time.sleep(.3); r.snap('09_draft_in_tool')
r.settle(still=5, limit=60, approve=False); r.mark('draft_after_tool'); r.snap('10_draft_after_tool')
r.keys('C-u'); time.sleep(3); r.snap('11_idle_end')
r.end()
