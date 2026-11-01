"""The cases model.py leaves out, needing no turn: python3 model2.py <name> <cols> <rows>."""
import os, sys, tempfile, time, uuid
from rec import *
name,cols,rows=sys.argv[1],int(sys.argv[2]),int(sys.argv[3])
CONFIG=os.environ['MODEL_CONFIG']
CLAUDE=os.path.expanduser('~/.local/share/claude/versions/2.1.316')
if os.path.exists(CONFIG+'/settings.json'): os.remove(CONFIG+'/settings.json')
sid=str(uuid.uuid4()); env=f'CLAUDE_CONFIG_DIR={CONFIG}'
r=Rec(name,cols=cols,rows=rows,claude=CLAUDE,env=env,args=f'--safe-mode --model haiku --permission-mode manual --session-id {sid}')
def step(s,label,*keys,text=None):
    s.mark(label)
    if text: s.say(text)
    if keys: s.keys(*keys)
    s.settle(still=2,limit=20,approve=False); s.snap(label)
r.trust(); time.sleep(3)
step(r,'ready'); step(r,'menu',text='/model'); 
if 'Select model' not in r.screen(): step(r,'menu2',text='/model')
step(r,'down','Down'); step(r,'session_only','s')
step(r,'arg_default',text='/model default')
step(r,'arg_1m',text='/model opus[1m]')
step(r,'arg_sonnet',text='/model sonnet')
r.keys('C-c'); time.sleep(.5); r.keys('C-c'); time.sleep(2); r.tmux('kill-server')
real=tempfile.mkdtemp; tempfile.mkdtemp=lambda **k: r.work
r2=Rec(name+'-resume',cols=cols,rows=rows,claude=CLAUDE,env=env,args=f'--safe-mode --permission-mode manual --resume {sid}')
tempfile.mkdtemp=real
time.sleep(6); r2.snap('resumed')
r2.end()
