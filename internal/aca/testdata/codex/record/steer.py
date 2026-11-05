"""Records what the wdx doc CODEX_STEER reads: a message steered into a turn while
Codex thinks, which shows a while above the box before Codex takes it in.

    python3 steer.py <name> <cols> <rows>
"""
import sys, time
from rec import Rec

name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
r = Rec(name, cols, rows, '--dangerously-bypass-approvals-and-sandbox')


def put(text, key):
    """As wdx's Deliver (Tab) or Steer (Enter) types a message."""
    r.paste(text)
    time.sleep(0.15)
    r.keys(key)


r.wait(r'^› (?:.*\n)+?\s{2}\S.* · ')
r.still(2)
r.snap('started')
# A pause after a mark, so it reads its step's screen, not the next key's.
time.sleep(1.5)
put('Think carefully about the 5 largest prime numbers below 1000, then list them and reply DONE.', 'Tab')
r.wait(r'esc to interrupt\)', 60)
time.sleep(0.5)
r.snap('working')
time.sleep(1.5)
put('Also add the word PURPLE at the end.', 'Enter')
# Shown until Codex takes it in, about 2 s while it thinks; marked at once.
if r.wait(r'Messages to be submitted after next tool call', 20):
    time.sleep(0.3)
    r.snap('steer_pending')
r.wait(r'PURPLE(?:.*\n)+?\s+Worked for', 120)
time.sleep(0.4)
r.snap('steer_done')
time.sleep(1.5)
r.end()
