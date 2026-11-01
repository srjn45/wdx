"""What model a resumed session really has after a /model no reply followed: python3 model3.py <name> <reply|noreply>."""
import os, sys, tempfile, time, uuid
from rec import *
name,kind=sys.argv[1],sys.argv[2]
CONFIG=os.environ['MODEL_CONFIG']
CLAUDE=os.path.expanduser('~/.local/share/claude/versions/2.1.316')
if os.path.exists(CONFIG+'/settings.json'): os.remove(CONFIG+'/settings.json')
sid=str(uuid.uuid4()); env=f'CLAUDE_CONFIG_DIR={CONFIG}'
r=Rec(name,claude=CLAUDE,env=env,args=f'--safe-mode --model haiku --permission-mode manual --session-id {sid}')
def step(s,label,*keys,text=None,still=2):
    s.mark(label)
    if text: s.say(text)
    if keys: s.keys(*keys)
    s.settle(still=still,limit=40,approve=False); s.snap(label)
r.trust(); time.sleep(3); step(r,'ready'); r.keys('C-u')
if kind=='reply': step(r,'turn',text='Say hi in one word.',still=4)
step(r,'to_sonnet',text='/model sonnet')
if 'Set model' not in r.screen(): step(r,'to_sonnet2',text='/model sonnet')
r.keys('C-c'); time.sleep(.5); r.keys('C-c'); time.sleep(2); r.tmux('kill-server')
real=tempfile.mkdtemp; tempfile.mkdtemp=lambda **k: r.work
r2=Rec(name+'-resume',claude=CLAUDE,env=env,args=f'--safe-mode --permission-mode manual --resume {sid}')
tempfile.mkdtemp=real
time.sleep(6); r2.snap('resumed')
step(r2,'menu',text='/model')
if 'Select model' not in r2.screen(): step(r2,'menu2',text='/model')
r2.snap('menu_screen'); step(r2,'esc','Escape')
r2.end()
