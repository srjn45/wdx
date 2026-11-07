"""status.py <name> <cols> <rows> [user]: what the parser reads with wdx's status line on.

The statusLine wdx gives in --settings (the wdx doc ANTIGRAVITY_CONTEXT): its command
keeps the JSON in a file and prints nothing, with stack_with_default so that agy's own
footer, which the parser reads, stays. The steps are turns.py's, less the message sent
at once, so the same table checks them: ready, a draft, the / menu, a turn, a message
queued in it, a background task during and after a turn, and an interrupt with Esc.
With user, a stand-in for the user's own status line follows wdx's in braces, as wdx
chains it: agy draws what it prints under its footer.
"""
import os, sys, time
from rec import Rec

name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
status = os.path.join(os.environ.get('AGY_REC_WORK', '/tmp/agyrec'), name + '-status.json')
command = f"f='{status}'; cat > \"$f.$$\" && mv \"$f.$$\" \"$f\""
if len(sys.argv) > 4 and sys.argv[4] == 'user':
    command += '; {\necho "their line: $(wc -c) bytes" # their comment\n} < "$f"'
r = Rec(name, cols, rows, settings={"statusLine": {"type": "command", "command": command, "stack_with_default": True}}, settings_file=True)
r.wait(r'^>', 20); r.snap('box_drawn')
r.wait(r'Flash'); time.sleep(0.3); r.snap('ready')
r.still(2)
r.type('a draft'); time.sleep(0.6); r.snap('draft')
r.keys('C-u'); time.sleep(0.4)
r.type('/'); time.sleep(0.8); r.snap('slash_menu')
r.keys('Escape'); time.sleep(0.3); r.keys('C-u'); time.sleep(0.5); r.snap('rest')

r.send('Run the shell command `sleep 3` and wait for it, then write a 6-line poem about rivers.')
r.wait(r'Generating'); time.sleep(0.3); r.snap('generating')
r.wait(r'Running command'); time.sleep(0.5); r.snap('running_command')
r.send('Then reply LIMA.'); time.sleep(0.8); r.snap('queued')
r.wait(r'^  LIMA', 90); time.sleep(1.5); r.snap('turn_done')

r.send('Start the shell command `sleep 25` in the background without waiting for it, then run `sleep 5` and wait for that, then reply PAPA.')
r.wait(r'task\(s\)', 40); time.sleep(0.3); r.snap('task_in_turn')
r.wait(r'^\? for shortcuts.*task', 60); time.sleep(0.3); r.snap('task_after_turn')
r.wait(r'^  PAPA', 60); r.wait(r'^\? for shortcuts +Gemini[^\n]*low$', 60); time.sleep(0.3); r.snap('task_over')
r.still(3); r.snap('task_over_still')

r.send('Run the shell command `sleep 30` and wait for it, then reply OSCAR.')
r.wait(r'Running command'); time.sleep(0.5); r.snap('before_esc')
r.keys('Escape'); time.sleep(1.5); r.snap('interrupted')
r.end()
os.remove(status)
print('done', name)
