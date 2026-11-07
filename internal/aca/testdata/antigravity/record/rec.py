"""Driver: a private tmux server, agy under script, in a new git directory.

agy is started as wdx starts it (the wdx doc ACA_ANTIGRAVITY): the cheapest model,
the work directory trusted for this session only, messages queued, and in full mode
no permission prompts and no terminal sandbox. The launch note is a folder given with
--add-dir holding AGENTS.md and, unless hooks=False, a .agents/hooks.json whose
hooks append each event's JSON to hooks.jsonl beside it. A scenario calls snap(name)
at each step it wants kept: the step's time goes to marks.log and its screen to
NN_name.txt (and .esc, with colours). Everything lands in $AGY_REC_OUT/<name>/, by
default out/<name>/ beside this file, which is not to be committed: copy a recording
into ../<version>/ with copy.py.
"""
import json, os, re, shutil, subprocess, tempfile, time

SP = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get('AGY_REC_OUT', os.path.join(SP, 'out'))
WORK = os.environ.get('AGY_REC_WORK', '/tmp/agyrec')
MODEL = os.environ.get('AGY_REC_MODEL', 'gemini-3.8-flash-low')


class Rec:
    def __init__(s, name, cols=120, rows=40, full=True, extra='', hooks=True, note='Answer briefly.', settings=None, settings_file=False):
        s.more = settings or {}
        s.settings_file = settings_file
        s.name = name
        s.sock = 'agyrec-' + name
        s.out = os.path.join(OUT, name)
        shutil.rmtree(s.out, ignore_errors=True)
        os.makedirs(s.out)
        os.makedirs(WORK, exist_ok=True)
        s.base = tempfile.mkdtemp(prefix='a', dir=WORK)
        s.work = os.path.join(s.base, 'ws')
        os.makedirs(s.work)
        subprocess.run(['git', 'init', '-q', s.work], check=True)
        s.note = os.path.join(s.base, 'note')
        os.makedirs(os.path.join(s.note, '.agents'))
        with open(os.path.join(s.note, 'AGENTS.md'), 'w') as f:
            f.write(note + '\n')
        s.hooklog = os.path.join(s.base, 'hooks.jsonl')
        if hooks:
            def h(ev):
                return [{"type": "command", "command": "{ printf '%s ' %s; tr -d '\\n'; echo; } >> '%s'; echo '{}'" % ('%s', ev, s.hooklog)}]
            spec = {"wdx": {ev: h(ev) for ev in ['SessionStart', 'PreInvocation', 'PostInvocation', 'Stop']}}
            spec["wdx"]["PostToolUse"] = [{"matcher": "*", "hooks": h('PostToolUse')}]
            with open(os.path.join(s.note, '.agents', 'hooks.json'), 'w') as f:
                json.dump(spec, f, indent=1)
        s.n = 0
        s.cols, s.rows = cols, rows
        s.full = full
        s.extra = extra
        s.start()

    def args(s, more=''):
        settings = {"queuedMessages": "queue", "trustedWorkspaces": [s.work]}
        settings.update(s.more)
        a = f"--model {MODEL}"
        if s.full:
            a += " --dangerously-skip-permissions"
            settings["enableTerminalSandbox"] = False
        given = "'" + json.dumps(settings) + "'"
        if s.settings_file:
            # The shell tmux runs the command in would expand a $ in an inline value.
            given = os.path.join(s.base, 'settings.json')
            with open(given, 'w') as f:
                json.dump(settings, f)
        a += f" --add-dir {s.note} --settings {given} {s.extra} {more}"
        return a

    def start(s, more='', raw='raw', cwd=None):
        cmd = (f"cd {cwd or s.work} && exec script -q -f --log-timing {s.out}/{raw}.timing "
               f"--log-out {s.out}/{raw}.out -c \"agy {s.args(more).replace(chr(34), chr(92) + chr(34))}\"")
        subprocess.run(['tmux', '-L', s.sock, 'new-session', '-d', '-s', 'r',
                        '-x', str(s.cols), '-y', str(s.rows), cmd], check=True)
        # The marks' clock is script's, whose timing log starts when it starts: on a
        # loaded machine that is well after tmux returns.
        while not os.path.exists(f'{s.out}/{raw}.timing'):
            time.sleep(0.01)
        s.t0 = time.time()

    def tmux(s, *a):
        return subprocess.run(['tmux', '-L', s.sock, *a], capture_output=True, text=True).stdout

    def screen(s, esc=False):
        return s.tmux('capture-pane', '-p', '-t', 'r', *(['-e'] if esc else []))

    def snap(s, name):
        s.n += 1
        with open(f'{s.out}/marks.log', 'a') as f:
            f.write(f'{time.time() - s.t0:.3f} {name}\n')
        with open(f'{s.out}/{s.n:02d}_{name}.txt', 'w') as f:
            f.write(s.screen())
        with open(f'{s.out}/{s.n:02d}_{name}.esc', 'w') as f:
            f.write(s.screen(True))
        print('saved', s.n, name, flush=True)

    def wait(s, pat, limit=90):
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
        s.tmux('set-buffer', '-b', 'p', text)
        s.tmux('paste-buffer', '-p', '-d', '-b', 'p', '-t', 'r')

    def send(s, text, key='Enter'):
        s.paste(text)
        time.sleep(0.15)
        s.keys(key)

    def still(s, quiet=3, limit=120):
        end = time.time() + limit
        last, since = None, time.time()
        while time.time() < end:
            c = s.screen()
            if c != last:
                last, since = c, time.time()
            if time.time() - since >= quiet:
                return
            time.sleep(0.1)

    def alive(s):
        return subprocess.run(['tmux', '-L', s.sock, 'has-session', '-t', 'r'], capture_output=True).returncode == 0

    def kill(s):
        """Hangs up agy, as wdx stops a terminal; it stops its commands with it."""
        s.tmux('kill-server')

    def end(s):
        s.kill()
        shutil.rmtree(s.base, ignore_errors=True)
        print('done', s.name, flush=True)

    def hooks(s):
        try:
            return [l.split(' ', 1) for l in open(s.hooklog)]
        except FileNotFoundError:
            return []
