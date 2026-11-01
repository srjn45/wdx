"""Typing /model slowly with older names above, where the suggestions shrink the conversation: python3 model5.py <name> <cols> <rows>."""
import json, os, sys, time
from rec import *
name,cols,rows=sys.argv[1],int(sys.argv[2]),int(sys.argv[3])
CONFIG=os.environ['MODEL_CONFIG']
CLAUDE=os.path.expanduser('~/.local/share/claude/versions/2.1.316')
if os.path.exists(CONFIG+'/settings.json'): os.remove(CONFIG+'/settings.json')
os.makedirs(f'{SP}/settings',exist_ok=True); tui=f'{SP}/settings/fullscreen.json'; json.dump({'tui':'fullscreen'},open(tui,'w'))
r=Rec(name,cols=cols,rows=rows,claude=CLAUDE,env=f'CLAUDE_CONFIG_DIR={CONFIG}',args=f'--safe-mode --model haiku --permission-mode manual --settings {tui}')
def settle(): r.settle(still=1.5,limit=20,approve=False)
r.trust(); time.sleep(3); settle(); r.keys('C-u'); time.sleep(.5)
r.say('/model sonnet'); settle()
if 'Set model' not in r.screen(): r.say('/model sonnet'); settle()
r.say('/model opus'); settle(); r.snap('opus')
r.tmux('send-keys','-t','r','-l','/'); settle(); r.snap('slash')
for ch in 'model':
    r.tmux('send-keys','-t','r','-l',ch); time.sleep(.3)
settle(); r.snap('typed')
r.keys('C-u'); settle(); r.snap('cleared')
for i in range(8):
    r.tmux('send-keys','-t','r','-l',f'line{i}\\'); time.sleep(.05); r.keys('Enter'); time.sleep(.1)
settle(); r.snap('tall_draft')
r.keys('C-c'); time.sleep(1.6); settle(); r.snap('tall_cleared')
r.end()
