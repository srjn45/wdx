"""steer.py <name> <cols> <rows>: messages sent at once with alt+m while agy thinks, streams, and runs a command."""
import time, sys
from rec import Rec
name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
r = Rec(name, cols, rows, hooks=True)
r.wait(r'Flash'); time.sleep(1)
r.send('Write a 400-word essay about mountains, with no tools.')
r.wait(r'Generating'); time.sleep(0.3)
r.paste('Also reply XRAY.'); time.sleep(0.15); r.keys('M-m'); time.sleep(0.5); r.snap('steer_generating')
r.wait(r'^  XRAY', 90); time.sleep(1); r.snap('steer_generating_done')
r.still(3)
r.send('Run the shell command `sleep 6`, then reply YANKEE.')
r.wait(r'Running command'); time.sleep(0.5)
r.paste('Also reply ZULU.'); time.sleep(0.15); r.keys('M-m'); time.sleep(0.5); r.snap('steer_command')
r.wait(r'ZULU', 60); time.sleep(2); r.snap('steer_command_done')
r.end()
