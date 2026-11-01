"""What shows old /model lines: fullscreen scrolled back, and the transcript view. python3 model4.py <name> <cols> <rows>."""
import json, os, sys, time
from rec import *
name,cols,rows=sys.argv[1],int(sys.argv[2]),int(sys.argv[3])
CONFIG=os.environ['MODEL_CONFIG']
CLAUDE=os.path.expanduser('~/.local/share/claude/versions/2.1.316')
if os.path.exists(CONFIG+'/settings.json'): os.remove(CONFIG+'/settings.json')
os.makedirs(f'{SP}/settings',exist_ok=True); tui=f'{SP}/settings/fullscreen.json'; json.dump({'tui':'fullscreen'},open(tui,'w'))
r=Rec(name,cols=cols,rows=rows,claude=CLAUDE,env=f'CLAUDE_CONFIG_DIR={CONFIG}',args=f'--safe-mode --model haiku --permission-mode manual --settings {tui}')
def step(s,label,*keys,text=None):
    s.mark(label)
    if text: s.say(text)
    if keys: s.keys(*keys)
    s.settle(still=2,limit=20,approve=False); s.snap(label)
r.trust(); time.sleep(3); step(r,'ready'); r.keys('C-u'); time.sleep(.5)
r.say('/model sonnet'); r.settle(still=2,limit=20,approve=False)
if 'Set model' not in r.screen(): r.say('/model sonnet'); r.settle(still=2,limit=20,approve=False)
for m in ['haiku','sonnet','haiku','sonnet','haiku','opus']:
    r.say('/model '+m); r.settle(still=1.5,limit=20,approve=False)
r.snap('switched')
step(r,'pgup','PPage'); step(r,'pgup2','PPage'); step(r,'bottom','C-End')
step(r,'transcript','C-o'); step(r,'transcript_end','Escape')
r.end()
