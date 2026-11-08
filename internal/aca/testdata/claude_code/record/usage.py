"""Records the /usage screen (the wdx doc USAGE, step 1): python3 usage.py <name> <cols> <rows>.

Run it from a copy outside the repository, as the README says: inside it Claude Code
asks about CLAUDE.md's external imports after the trust prompt, and the keys go there.
"""
import os, sys, time
from rec import *
name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
cl = os.path.expanduser('~/.local/share/claude/versions/2.1.319')
r = Rec(name, cols=cols, rows=rows, claude=cl if os.path.exists(cl) else CLAUDE)
r.trust()
r.wait(r'❯ Try "|❯ *$', 30, where=3); time.sleep(2); r.snap('idle')
r.tmux('send-keys', '-t', 'r', '-l', '/usage'); time.sleep(1.5); r.snap('slash_menu')
r.keys('Enter')
r.wait(r'(?i)(session|week|usage|limit|resets)', 30, where=30); time.sleep(5); r.snap('usage')
r.keys('Right'); time.sleep(1.5); r.snap('usage_right')
r.keys('Tab'); time.sleep(1.5); r.snap('usage_tab')
r.keys('Escape'); time.sleep(1.5); r.snap('after_esc')
r.end()
