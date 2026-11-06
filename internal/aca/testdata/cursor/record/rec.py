"""Driver: a private tmux server, cursor-agent under script, in a new git directory.

Cursor runs with HOME set to $CURSOR_REC_HOME, a scratch home holding a copy of the
user's ~/.config/cursor/auth.json (its login) and ~/.cursor/cli-config.json, so that
nothing here touches the user's ~/.cursor: --model rewrites the default model in the
config, --trust saves the work directory's trust, and every chat is kept. The work
directories are under that home, so Cursor shows them as ~/w/<random>, short and
private. $CURSOR_REC_ENV is added to the environment, as CURSOR_CONFIG_DIR=<dir>.

A scenario makes one Rec and calls snap(name) at each step it wants kept: the step's
time goes to marks.log, its screen to NN_name.txt and, with its attributes, NN_name.ansi.
Everything lands in $CURSOR_REC_OUT/<name>/, by default out/<name>/ beside this file,
which is not to be committed: copy a recording into ../<version>/ with copy.py.
"""
import os, re, shutil, subprocess, tempfile, time

HERE = os.path.dirname(os.path.abspath(__file__))
HOME = os.environ['CURSOR_REC_HOME']
OUT = os.environ.get('CURSOR_REC_OUT', os.path.join(HERE, 'out'))
BIN = os.environ.get('CURSOR_REC_BIN', 'cursor-agent')
WORK = os.path.join(HOME, 'w')


class Rec:
    def __init__(s, name, cols=120, rows=40, args='', work=None):
        s.name, s.sock = name, 'currec-' + name
        s.out = os.path.join(OUT, name)
        shutil.rmtree(s.out, ignore_errors=True)
        os.makedirs(s.out)
        os.makedirs(WORK, exist_ok=True)
        if work:
            s.work = work
        else:
            s.work = tempfile.mkdtemp(prefix='w', dir=WORK)
            subprocess.run(['git', 'init', '-q', s.work], check=True)
        s.n, s.cols, s.rows = 0, cols, rows
        s.start(args)

    def start(s, args, raw='raw'):
        """Starts cursor-agent in the work directory, recording to <raw>.out and <raw>.timing."""
        cur = f"{BIN} {args}"
        env = os.environ.get('CURSOR_REC_ENV', '')
        cmd = (f"cd {s.work} && export HOME={HOME} {env} && exec script -q -f --log-timing {s.out}/{raw}.timing "
               f"--log-out {s.out}/{raw}.out -c \"{cur.replace(chr(34), chr(92) + chr(34))}\"")
        if subprocess.run(['tmux', '-L', s.sock, 'has-session', '-t', 'r'], capture_output=True).returncode == 0:
            subprocess.run(['tmux', '-L', s.sock, 'respawn-pane', '-k', '-t', 'r', cmd], check=True)
        else:
            subprocess.run(['tmux', '-L', s.sock, 'new-session', '-d', '-s', 'r',
                            '-x', str(s.cols), '-y', str(s.rows), cmd], check=True)
            s.tmux('set-option', '-t', 'r', 'remain-on-exit', 'on')
        # script's clock starts about now; marks a little early read a screen before its step.
        s.t0 = time.time()

    def tmux(s, *a):
        return subprocess.run(['tmux', '-L', s.sock, *a], capture_output=True, text=True).stdout

    def screen(s, attributes=False):
        return s.tmux('capture-pane', '-p', '-t', 'r', *(['-e'] if attributes else []))

    def lines(s):
        return s.screen().rstrip('\n').split('\n')

    def snap(s, name):
        s.n += 1
        with open(f'{s.out}/marks.log', 'a') as f:
            f.write(f'{time.time() - s.t0:.3f} {name}\n')
        open(f'{s.out}/{s.n:02d}_{name}.txt', 'w').write(s.screen())
        open(f'{s.out}/{s.n:02d}_{name}.ansi', 'w').write(s.screen(True))
        print('saved', s.n, name, flush=True)

    def wait(s, pat, limit=60):
        end = time.time() + limit
        while time.time() < end:
            if re.search(pat, s.screen(), re.M):
                return True
            time.sleep(0.05)
        print('TIMEOUT', pat, flush=True)
        s.snap('timeout')
        return False

    def keys(s, *k):
        s.tmux('send-keys', '-t', 'r', *k)

    def type(s, text):
        s.tmux('send-keys', '-t', 'r', '-l', text)

    def paste(s, text):
        """A bracketed paste, as wdx's Deliver sends it."""
        s.tmux('set-buffer', '-b', 'p', text)
        s.tmux('paste-buffer', '-p', '-d', '-b', 'p', '-t', 'r')

    def send(s, text, key='Enter'):
        """As wdx delivers a message: a paste, 150 ms, then Enter."""
        s.paste(text)
        time.sleep(0.15)
        s.keys(key)

    def still(s, quiet=3, limit=120):
        """Waits until the screen stops changing: a driver's guess, not the truth."""
        end = time.time() + limit
        last, since = None, time.time()
        while time.time() < end:
            c = s.screen()
            if c != last:
                last, since = c, time.time()
            if time.time() - since >= quiet:
                return
            time.sleep(0.1)

    def kill(s):
        """Hangs Cursor up as wdx stops a terminal. A command it ran mid-turn can outlive it."""
        s.tmux('kill-server')
