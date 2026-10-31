"""Driver: a private tmux server, claude under script, no inherited Claude env."""
import os, re, shutil, subprocess, tempfile, threading, time, json
SP=os.path.dirname(os.path.abspath(__file__))
CLAUDE=os.path.expanduser('~/.local/share/claude/versions/2.1.314')
class Rec:
    def __init__(s,name,cols=120,rows=40,args='--safe-mode --model haiku --permission-mode manual',env='',claude=CLAUDE):
        s.name=name; s.sock='wdxrec-'+name; s.out=f'{SP}/out/{name}'; shutil.rmtree(s.out,ignore_errors=True); os.makedirs(s.out)
        s.work=tempfile.mkdtemp(prefix='w-',dir=f'{SP}/work' if os.path.isdir(f'{SP}/work') else (os.makedirs(f'{SP}/work') or f'{SP}/work'))
        subprocess.run(['git','init','-q',s.work],check=True)
        s.n=0; s.t0=time.time(); s.label='start'; s.stop=threading.Event()
        cmd=(f"cd {s.work} && exec env -u CLAUDECODE -u CLAUDE_PID -u CLAUDE_EFFORT -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_MESSAGING_SOCKET "
             f"-u CLAUDE_CODE_MESSAGING_TOKEN -u CLAUDE_CODE_EXECPATH -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CODE_SESSION_ATTENDED "
             f"DISABLE_AUTOUPDATER=1 {env} script -q -f --log-timing {s.out}/raw.timing --log-out {s.out}/raw.out --log-in {s.out}/raw.in -c '{claude} {args}'")
        subprocess.run(['tmux','-L',s.sock,'new-session','-d','-s','r','-x',str(cols),'-y',str(rows),cmd],check=True)
        s.log=open(f'{s.out}/steps.log','w')
        threading.Thread(target=s.watch,daemon=True).start()
    def tmux(s,*a): return subprocess.run(['tmux','-L',s.sock,*a],capture_output=True,text=True).stdout
    def screen(s): return s.tmux('capture-pane','-p','-t','r')
    def nb(s): return [l for l in s.screen().split('\n') if l.strip()]
    def bottom(s,n=1):
        l=s.nb(); return '\n'.join(x.strip() for x in l[-n:])
    def watch(s):
        prev=None
        while not s.stop.is_set():
            c=s.bottom(6)
            if c!=prev:
                s.log.write(f'{time.time()-s.t0:7.1f} {s.label:20} {c!r}\n'); s.log.flush(); prev=c
            time.sleep(0.1)
    def mark(s,l): s.label=l; s.log.write(f'{time.time()-s.t0:7.1f} MARK {l}\n'); s.log.flush()
    def snap(s,name):
        s.n+=1
        open(f'{s.out}/marks.log','a').write(f'{time.time()-s.t0:.3f} {name}\n')
        open(f'{s.out}/{s.n:02d}_{name}.txt','w').write(s.screen()); print('saved',s.n,name,'|',s.bottom(1)[:100],flush=True)
    def wait(s,pat,limit=60,where=8):
        end=time.time()+limit
        while time.time()<end:
            if re.search(pat,'\n'.join(s.nb()[-where:]),re.S): return True
            time.sleep(0.1)
        print('TIMEOUT',pat,flush=True); s.snap('timeout'); return False
    def keys(s,*k): s.tmux('send-keys','-t','r',*k)
    def say(s,t,settle=0.5):
        s.tmux('send-keys','-t','r','-l',t); time.sleep(settle); s.keys('Enter')
    def trust(s):
        if s.wait('Enter to confirm',20,where=10):
            time.sleep(1); s.keys('Down','Enter')
        s.wait('❯',20)
    def turn_over(s,limit=120):
        time.sleep(1.5); end=time.time()+limit
        while time.time()<end:
            b='\n'.join(s.nb()[-8:])
            if 'esc to interrupt' not in b and 'Esc to cancel' not in b: return True
            time.sleep(0.1)
        print('TIMEOUT turn',flush=True); s.snap('timeout'); return False
    def settle(s,still=4,limit=90,approve=True):
        '''Waits until the screen stops changing, answering prompts; a driver's guess, not the truth.'''
        end=time.time()+limit; last=None; since=time.time()
        while time.time()<end:
            b=s.nb()
            if approve and any('Esc to cancel' in x or x.strip()=='to cancel' for x in b[-3:]): s.keys('Enter'); time.sleep(1.2); since=time.time(); continue
            c=s.screen()
            if c!=last: last=c; since=time.time()
            if time.time()-since>=still: return
            time.sleep(.1)
    def slow(s,text,gap=.25):
        for ch in text:
            s.tmux('send-keys','-t','r','-l',ch); time.sleep(gap)
    def end(s):
        s.stop.set(); s.keys('C-c'); time.sleep(.5); s.keys('C-c'); time.sleep(2)
        s.tmux('kill-server')
        slug=re.sub(r'[^A-Za-z0-9-]','-',s.work)
        import glob
        for f in glob.glob(os.path.expanduser('~/.claude/projects/'+slug+'/*.jsonl')): shutil.copy(f,s.out+'/transcript.jsonl')
        # Claude keeps a transcript per directory; this one's has served its purpose.
        shutil.rmtree(os.path.expanduser('~/.claude/projects/'+slug),ignore_errors=True)
        shutil.rmtree(s.work,ignore_errors=True); print('done',s.name,flush=True)
