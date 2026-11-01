"""What /model and the commands around it show: python3 model.py <name> <cols> <rows> <default|fullscreen>.
Run with CLAUDE_CONFIG_DIR of its own (CONFIG below): /model may save the choice to the user's settings."""
import json, os, shutil, sys, tempfile, time, uuid
from rec import *
name,cols,rows,tui=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]),sys.argv[4]
CONFIG=os.environ['MODEL_CONFIG']
CLAUDE=os.path.expanduser('~/.local/share/claude/versions/2.1.316')
os.makedirs(f'{SP}/settings',exist_ok=True); tuifile=f'{SP}/settings/{tui}.json'
json.dump({'tui':tui},open(tuifile,'w'))
if os.path.exists(CONFIG+'/settings.json'): os.remove(CONFIG+'/settings.json')
sid=str(uuid.uuid4())
r=Rec(name,cols=cols,rows=rows,claude=CLAUDE,env=f'CLAUDE_CONFIG_DIR={CONFIG}',args=f'--safe-mode --model haiku --permission-mode manual --settings {tuifile} --session-id {sid}')
def saved(s,label):
    p=CONFIG+'/settings.json'; text=open(p).read() if os.path.exists(p) else '(none)'
    open(f'{s.out}/settings.log','a').write(f'{label}: {text}\n')
def step(s,label,*keys,text=None,still=2,limit=30):
    s.mark(label)
    if text: s.say(text)
    if keys: s.keys(*keys)
    s.settle(still=still,limit=limit,approve=False); s.snap(label); saved(s,label)
r.trust(); time.sleep(3); r.snap('banner'); saved(r,'banner')
step(r,'menu_open',text='/model')
step(r,'menu_esc','Escape')
step(r,'menu_again',text='/model')
step(r,'menu_down','Down')
step(r,'menu_pick','Enter')
step(r,'arg_sonnet',text='/model sonnet')
step(r,'arg_haiku',text='/model haiku')
step(r,'arg_bad',text='/model nonsense')
step(r,'turn',text='Say hi in one word.',still=4,limit=60)
step(r,'clear',text='/clear')
step(r,'turn2',text='Say ok in one word.',still=4,limit=60)
step(r,'compact',text='/compact',still=5,limit=90)
step(r,'to_sonnet',text='/model sonnet')
r.keys('C-c'); time.sleep(.5); r.keys('C-c'); time.sleep(2); r.tmux('kill-server')
real=tempfile.mkdtemp; tempfile.mkdtemp=lambda **k: r.work
r2=Rec(name+'-resume',cols=cols,rows=rows,claude=CLAUDE,env=f'CLAUDE_CONFIG_DIR={CONFIG}',args=f'--safe-mode --permission-mode manual --settings {tuifile} --resume {sid}')
tempfile.mkdtemp=real
time.sleep(6); r2.snap('resumed'); saved(r2,'resumed')
step(r2,'menu_open',text='/model')
step(r2,'menu_esc','Escape')
slug=re.sub(r'[^A-Za-z0-9-]','-',r2.work)
import glob
for f in glob.glob(f'{CONFIG}/projects/{slug}/*.jsonl'): shutil.copy(f,r2.out+'/transcript.jsonl')
r2.end()
