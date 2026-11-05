"""Driver: a private tmux server, codex under script, in a new git directory.

A scenario makes one Rec and calls snap(name) at each step it wants kept: the
step's time goes to marks.log and its screen to NN_name.txt. Everything lands in
$CODEX_REC_OUT/<name>/, by default out/<name>/ beside this file, which is not to be
committed: copy a recording into ../<version>/ with copy.py.
"""
import os, re, shutil, subprocess, tempfile, threading, time

SP = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get('CODEX_REC_OUT', os.path.join(SP, 'out'))
# Codex shows its directory, so the work directory is short and says nothing private.
WORK = os.environ.get('CODEX_REC_WORK', '/tmp/codexrec')


class Rec:
    def __init__(s, name, cols=120, rows=40, args=''):
        s.name = name
        s.sock = 'wdxrec-' + name
        s.out = os.path.join(OUT, name)
        shutil.rmtree(s.out, ignore_errors=True)
        os.makedirs(s.out)
        os.makedirs(WORK, exist_ok=True)
        s.work = tempfile.mkdtemp(prefix='w', dir=WORK)
        subprocess.run(['git', 'init', '-q', s.work], check=True)
        s.n = 0
        # As wdx starts it: no update offer, the directory trusted for this session only.
        trust = 'projects={"%s"={trust_level="trusted"}}' % s.work
        codex = f"codex -c check_for_update_on_startup=false -c '{trust}' {args}"
        cmd = (f"cd {s.work} && exec script -q -f --log-timing {s.out}/raw.timing "
               f"--log-out {s.out}/raw.out -c \"{codex.replace(chr(34), chr(92) + chr(34))}\"")
        subprocess.run(['tmux', '-L', s.sock, 'new-session', '-d', '-s', 'r',
                        '-x', str(cols), '-y', str(rows), cmd], check=True)
        # script's clock starts about now; marks a little early read a screen before its step.
        s.t0 = time.time()

    def tmux(s, *a):
        return subprocess.run(['tmux', '-L', s.sock, *a], capture_output=True, text=True).stdout

    def screen(s):
        return s.tmux('capture-pane', '-p', '-t', 'r')

    def lines(s):
        return s.screen().rstrip('\n').split('\n')

    def snap(s, name):
        s.n += 1
        with open(f'{s.out}/marks.log', 'a') as f:
            f.write(f'{time.time() - s.t0:.3f} {name}\n')
        with open(f'{s.out}/{s.n:02d}_{name}.txt', 'w') as f:
            f.write(s.screen())
        print('saved', s.n, name, flush=True)

    def wait(s, pat, limit=60):
        end = time.time() + limit
        while time.time() < end:
            if re.search(pat, s.screen(), re.M):
                return True
            time.sleep(0.02)
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

    def end(s):
        s.keys('C-c')
        time.sleep(0.5)
        s.keys('C-c')
        time.sleep(1.5)
        s.tmux('kill-server')
        shutil.rmtree(s.work, ignore_errors=True)
        print('done', s.name, flush=True)
