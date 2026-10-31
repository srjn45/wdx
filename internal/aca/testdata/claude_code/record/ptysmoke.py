# A holder-like environment, no tmux: TERM=xterm-256color, a bare PTY, typing only.
import os,pty,select,sys,time,subprocess,struct,fcntl,termios,re,tempfile,shutil
from rec import CLAUDE,SP
extra=dict(a.split('=',1) for a in sys.argv[2:])
work=tempfile.mkdtemp(prefix='p-',dir=SP+'/work'); subprocess.run(['git','init','-q',work],check=True)
env={k:v for k,v in os.environ.items() if not k.startswith('CLAUDE') and k not in('TMUX','TMUX_PANE','TERM_PROGRAM','TERM_PROGRAM_VERSION')}
env.update(TERM='xterm-256color',DISABLE_AUTOUPDATER='1',**extra)
pid,fd=pty.fork()
if pid==0:
    os.chdir(work); os.execve(CLAUDE,[CLAUDE,'--safe-mode','--model','haiku','--permission-mode','manual'],env)
fcntl.ioctl(fd,termios.TIOCSWINSZ,struct.pack('HHHH',40,120,0,0))
buf=b''; t0=time.time(); sent=[]
def pump(sec):
    global buf
    end=time.time()+sec
    while time.time()<end:
        r,_,_=select.select([fd],[],[],.1)
        if r:
            try: buf+=os.read(fd,65536)
            except OSError: return
pump(5); os.write(fd,b'\x1b[B\r'); pump(5); os.write(fd,b'hello'); pump(2); os.write(fd,b'\x15'); pump(1); os.write(fd,b'\x03'); pump(.5); os.write(fd,b'\x03'); pump(2)
open(f'{SP}/out/{sys.argv[1]}.raw','wb').write(buf)
print(len(buf),{k:buf.count(k) for k in [b'\x1b[?1049h',b'\x1b[?2026h',b'\x1b[?2026l',b'\x1b[?2026$p',b'\x1b[>0q',b'\x1b[c',b'\x1b[6n']})
os.kill(pid,9) if os.waitpid(pid,os.WNOHANG)==(0,0) else None
shutil.rmtree(work,ignore_errors=True)
