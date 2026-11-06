"""approval.py <name> <cols> <rows>: in ask mode, a command approval, then its denial with Esc."""
import sys, time
from rec import Rec
name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
# The user's own allow list would approve every command; an empty one for this session asks.
r = Rec(name, cols, rows, full=False, settings={"permissions": {"allow": []}})
r.wait(r'Flash'); time.sleep(1); r.snap('ready')
r.send('Run the shell command `date` and then reply DONE.')
r.wait(r'Run this command\?', 60); time.sleep(1); r.snap('approval')
r.keys('Escape'); time.sleep(2); r.snap('denied')
r.end()
