"""The decisions of one session, for the wdx doc PROMPT_TAB: a radio decision with
text typed in its Type something option, a checkbox decision with a box ticked,
text typed and the cursor on its Submit row, and a decision of two questions,
the first answered, then its review screen.

usage: [CLAUDE_VERSION=2.1.323] decisions.py <name> <cols> <rows>
"""
import sys, time
from rec import *

# A key right after a snap lands in the marked screen on replay: let it settle first.
_snap = Rec.snap
def snap(self, name):
    _snap(self, name); time.sleep(1.5)
Rec.snap = snap

name = sys.argv[1]; cols = int(sys.argv[2]); rows = int(sys.argv[3])
r = Rec(name, cols=cols, rows=rows, args='--safe-mode --model haiku --permission-mode manual',
        claude=os.path.expanduser('~/.local/share/claude/versions/' + os.environ.get('CLAUDE_VERSION', '2.1.323')))
if r.wait('Enter to confirm', 30, where=12):
    r.mark('trust'); time.sleep(2); r.snap('00_trust'); r.keys('Down'); time.sleep(0.5); r.keys('Enter')
r.wait('❯', 20); time.sleep(2); r.mark('idle'); time.sleep(1); r.snap('01_idle_start')

def asked():
    return r.wait('Enter to select|to cancel', 60, where=4)

def down(n):
    for _ in range(n):
        r.keys('Down'); time.sleep(0.4)

r.mark('turn_radio'); r.say('Use the AskUserQuestion tool to ask me to choose a color: red, blue or green, single choice. Do nothing else.', settle=1.5)
if asked():
    r.mark('decision_radio'); time.sleep(2); r.snap('02_decision_radio')
    down(3); time.sleep(1); r.snap('03_decision_radio_on_text')
    r.slow('teal', .15); time.sleep(1); r.snap('04_decision_radio_typed')
    down(1); time.sleep(1); r.snap('05_decision_radio_on_chat')
    r.keys('Escape'); r.mark('after_radio_esc'); time.sleep(3); r.snap('06_after_radio_esc')
r.turn_over(60)

r.mark('turn_checkbox'); r.say('Use the AskUserQuestion tool with multiSelect true to ask which fruits I like: apple, pear, plum. Do nothing else.', settle=1.5)
if asked():
    r.mark('decision_checkbox'); time.sleep(2); r.snap('07_decision_checkbox')
    r.keys('Space'); time.sleep(1.5); r.snap('08_decision_checkbox_ticked')
    down(3); r.slow('kiwi', .15); time.sleep(1); r.snap('09_decision_checkbox_typed')
    down(1); time.sleep(1); r.snap('10_decision_checkbox_on_submit')
    down(1); time.sleep(1); r.snap('11_decision_checkbox_past_submit')
    r.keys('Escape'); r.mark('after_checkbox_esc'); time.sleep(3); r.snap('12_after_checkbox_esc')
r.turn_over(60)

r.mark('turn_two'); r.say('Use the AskUserQuestion tool once, with two questions in the same call, single choice each: '
                          'header Color, which color: red or blue; header Size, which size: small or large. Do nothing else.', settle=1.5)
if asked():
    r.mark('decision_two'); time.sleep(2); r.snap('13_decision_two_first')
    r.keys('Enter'); time.sleep(2); r.snap('14_decision_two_answered')
    r.keys('Enter'); time.sleep(2); r.snap('15_decision_review')
    down(1); time.sleep(1); r.snap('16_decision_review_moved')
    r.keys('Up'); time.sleep(1); r.keys('Enter'); r.mark('after_review_submit'); time.sleep(3); r.snap('17_after_review_submit')
r.turn_over(60)
r.mark('idle_end'); time.sleep(2); r.snap('18_idle_end')
r.end()
