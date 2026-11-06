"""turns.py <name> <cols> <rows>: what the parser reads, in full mode.

Startup, the box empty, a draft, a multi-line draft, the / menu, a turn thinking,
running a command, a message queued during it, a message sent at once with alt+m
while a command runs (it goes to the background), the turn over while a background
task runs, an interrupt with Esc, and the end of a turn.
"""
import sys, time
from rec import Rec

name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
r = Rec(name, cols, rows)
r.wait(r'^>', 20); r.snap('box_drawn')
r.wait(r'Flash'); time.sleep(0.3); r.snap('ready')
r.still(2)
r.type('a draft'); time.sleep(0.6); r.snap('draft')
r.keys('C-u'); time.sleep(0.4)
r.paste('line one\nline two'); time.sleep(0.6); r.snap('draft_multi')
r.keys('C-u'); r.keys('C-u'); r.keys('BSpace'); r.keys('C-u'); time.sleep(0.4)
r.type('/'); time.sleep(0.8); r.snap('slash_menu')
r.keys('Escape'); time.sleep(0.3); r.keys('C-u'); time.sleep(0.5); r.snap('rest')

r.send('Run the shell command `sleep 3` and wait for it, then write a 6-line poem about rivers.')
r.wait(r'Generating'); r.snap('generating')
r.wait(r'Running command'); time.sleep(0.5); r.snap('running_command')
r.send('Then reply LIMA.'); time.sleep(0.8); r.snap('queued')
r.wait(r'^  LIMA', 90); time.sleep(1.5); r.snap('turn_done')

r.send('Run the shell command `sleep 9; echo bg-done` and wait for it, then reply MIKE.')
r.wait(r'Running command'); time.sleep(0.5)
r.paste('Also reply NOVEMBER.'); time.sleep(0.15); r.keys('M-m'); time.sleep(0.6); r.snap('sent_now_pending')
r.wait(r'^  NOVEMBER', 60); time.sleep(1.5); r.snap('sent_now_done')

r.send('Start the shell command `sleep 25` in the background without waiting for it, then run `sleep 5` and wait for that, then reply PAPA.')
r.wait(r'task\(s\)', 40); time.sleep(0.3); r.snap('task_in_turn')
r.wait(r'^\? for shortcuts.*task', 60); time.sleep(0.3); r.snap('task_after_turn')
r.wait(r'^  PAPA', 60); r.wait(r'^\? for shortcuts +Gemini[^\n]*low$', 60); time.sleep(0.3); r.snap('task_over')
r.still(3); r.snap('task_over_still')

r.send('Run the shell command `sleep 30` and wait for it, then reply OSCAR.')
r.wait(r'Running command'); time.sleep(0.5); r.snap('before_esc')
r.keys('Escape'); time.sleep(1.5); r.snap('interrupted')
r.end()
print('done', name)
