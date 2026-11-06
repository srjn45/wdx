"""sub.py <name> <cols> <rows>: a subagent running while the main turn has ended."""
import time, sys
from rec import Rec
r = Rec(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]))
r.wait(r'Flash'); time.sleep(1)
r.send('Start a subagent (use your tool for invoking a subagent) whose task is: run the shell command `sleep 15`, then reply SUBDONE. Wait for it, then reply ROMEO.')
for i in range(14):
    time.sleep(2.5); r.snap('sub%02d' % i)
r.wait(r'^  ROMEO', 90); time.sleep(1); r.snap('done')
r.end()
