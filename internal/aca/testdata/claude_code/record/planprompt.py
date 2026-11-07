"""The plan approval of one session in plan mode, with the PermissionRequest hook on.

usage: planprompt.py <name> <cols> <rows>
"""
import json, sys, time
from rec import *

# A key right after a snap lands in the marked screen on replay: let it settle first.
_snap = Rec.snap
def snap(self, name):
    _snap(self, name); time.sleep(1.5)
Rec.snap = snap

name = sys.argv[1]; cols = int(sys.argv[2]); rows = int(sys.argv[3])
out = f'{SP}/hooks/{name}'
os.makedirs(out, exist_ok=True)
hooks = f'{out}/hooks.jsonl'
log = [{'type': 'command', 'command': f'cat >> {hooks}; echo >> {hooks}'}]
settings = f'{out}/settings.json'
json.dump({'hooks': {'PermissionRequest': [{'matcher': '', 'hooks': log}],
                     'PreToolUse': [{'matcher': 'AskUserQuestion|ExitPlanMode', 'hooks': log}]}}, open(settings, 'w'))
r = Rec(name, cols=cols, rows=rows, args=f'--safe-mode --model haiku --permission-mode plan --settings {settings}',
        claude=os.path.expanduser('~/.local/share/claude/versions/' + os.environ.get('CLAUDE_VERSION', '2.1.319')))
if r.wait('Enter to confirm', 30, where=12):
    r.mark('trust'); time.sleep(2); r.snap('01_trust'); r.keys('Down'); time.sleep(0.5); r.keys('Enter')
r.wait('❯', 20); time.sleep(2); r.mark('idle'); time.sleep(1); r.snap('02_idle_plan_mode')
r.mark('turn_plan'); r.say('Plan: create hi.txt containing hi. No exploring. Call ExitPlanMode right away.', settle=1.5)
if r.wait('Esc to cancel|Would you like|ctrl\\+g to edit', 90, where=14):
    r.mark('plan_approval'); time.sleep(3); r.snap('03_plan_approval'); print(r.bottom(4), flush=True)
    r.keys('Escape'); r.mark('after_esc'); time.sleep(3); r.snap('04_after_plan_esc')
r.turn_over(30); r.mark('idle_end'); time.sleep(2); r.snap('05_idle_end')
r.end()
