"""The prompts of one session: trust, a Bash and a Write permission, a radio and a
checkbox decision, with hooks appending what Claude sends to out/<name>/hooks.jsonl.

usage: prompts.py <name> <cols> <rows>
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
                     'PreToolUse': [{'matcher': 'AskUserQuestion', 'hooks': log}]}}, open(settings, 'w'))
r = Rec(name, cols=cols, rows=rows, args=f'--safe-mode --model haiku --permission-mode manual --settings {settings}',
        claude=os.path.expanduser('~/.local/share/claude/versions/' + os.environ.get('CLAUDE_VERSION', '2.1.319')))
if r.wait('Enter to confirm', 30, where=12):
    r.mark('trust'); time.sleep(2); r.snap('01_trust'); r.keys('Down'); time.sleep(0.5); r.keys('Enter')
r.wait('❯', 20); time.sleep(2); r.mark('idle'); time.sleep(1); r.snap('02_idle_start')

r.mark('turn_bash'); r.say('Run this exact command with the Bash tool: touch wdx-a.txt', settle=1.5)
if r.wait('Esc to cancel', 60, where=3):
    r.mark('permission_bash'); time.sleep(2); r.snap('04_permission_bash')
    r.keys('Down'); time.sleep(1.5); r.snap('05_permission_bash_moved')
    r.keys('Escape'); r.mark('after_deny'); time.sleep(3); r.snap('06_after_deny')
r.turn_over(60)

r.mark('turn_write'); r.say('Create the file wdx-b.txt containing the word hi, with the Write tool.', settle=1.5)
if r.wait('Esc to cancel', 60, where=3):
    r.mark('permission_write'); time.sleep(2); r.snap('08_permission_write')
    r.keys('Escape'); r.mark('after_write_esc'); time.sleep(3); r.snap('09_after_write_esc')
r.turn_over(60)

r.mark('turn_radio'); r.say('Use the AskUserQuestion tool to ask me to choose a color: red or blue, single choice. Do nothing else.', settle=1.5)
if r.wait('Enter to select|to cancel', 60, where=3):
    r.mark('decision_radio'); time.sleep(2); r.snap('14_decision_radio')
    r.keys('Enter'); r.mark('radio_answered'); time.sleep(2); r.snap('15_radio_answered')
r.turn_over(60)

r.mark('turn_checkbox'); r.say('Use the AskUserQuestion tool with multiSelect true to ask which fruits I like: apple, pear, plum. Do nothing else.', settle=1.5)
if r.wait('Enter to select|to cancel', 60, where=3):
    r.mark('decision_checkbox'); time.sleep(2); r.snap('17_decision_checkbox')
    r.keys('Space'); time.sleep(1.5); r.snap('18_decision_checkbox_ticked')
    r.keys('Escape'); r.mark('after_checkbox_esc'); time.sleep(3); r.snap('19_after_checkbox_esc')
r.turn_over(60)
r.mark('idle_end'); time.sleep(2); r.snap('24_idle_end')
r.end()
