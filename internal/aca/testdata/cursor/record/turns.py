"""Records what the activity is read from: in full mode a tool turn with a queued follow-up,
a steer into a running tool, a background task outliving its turn, a streamed answer, a steer
pending while it streams, an interrupt with Ctrl+C and a subagent; in ask mode an approval."""
import sys, time
from rec import Rec
from frames import start_frames
name, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
mode = sys.argv[4] if len(sys.argv) > 4 else 'full'
args = '--trust --model gemini-3.8-flash-low' + (' --force --sandbox disabled --approve-mcps' if mode == 'full' else '')
r = Rec(name, cols, rows, args)
st = start_frames(r, 16)
STEP = 1.5
def step(n): r.snap(n); time.sleep(STEP)
def idle(limit=90):
    r.wait(r'→ Add a follow-up\s*\n\s*\n\s*\n\s*Gemini', limit); r.still(2)
r.wait('Plan, search', 30); time.sleep(1); step('started')
if mode == 'ask':
    r.send('Run the shell command `touch made.txt` and then reply DONE.')
    r.wait(r'Run this command\?', 40); time.sleep(1); step('approval')
    r.keys('Escape'); time.sleep(1); step('denied_tell')
    r.keys('Enter'); time.sleep(1); idle(); step('after_denial')
    r.kill(); sys.exit()
r.send('Run the shell command `sleep 8; echo SLEPT` and then reply with only the word DONE1.')
r.wait(r'\$ sleep 8', 30); time.sleep(1); step('tool_running')
r.send('After that, reply with only the word QUEUED.'); time.sleep(0.8); step('queued')
r.wait(r'^\s*QUEUED\s*$', 60); idle(); step('after_queue')
r.send('Run the shell command `sleep 20; echo BG` and then reply with only the word DONE2.')
r.wait(r'\$ sleep 20', 30); time.sleep(1)
r.send('Do not wait for it. Reply with only the word STEERED.'); time.sleep(0.6); r.keys('Enter'); time.sleep(0.4); step('steered')
r.wait(r'^\s*STEERED\s*$', 60); r.wait(r'→ Add a follow-up\s*\n\s*\n\s*\n\s*1 task', 30); time.sleep(0.5); step('idle_with_task')
r.wait(r'Finished', 60); time.sleep(0.3); step('task_finished_wakes')
idle(); step('after_wake')
r.send('Write a numbered list of 60 short lines about rivers. No tools.')
r.wait(r'^\s*1[0-9]\. ', 40); step('streaming')
r.send('Then reply with only the word GREEN.'); time.sleep(0.4); r.keys('Enter'); time.sleep(0.6); step('steer_pending')
time.sleep(1); r.keys('C-c'); time.sleep(0.6); step('interrupted')
time.sleep(4); step('interrupted_later')
r.keys('C-c'); time.sleep(3.6)
r.send('Use a subagent to run the shell command `echo SUB` and tell me what it printed. Then reply with only DONE3.')
r.wait(r'(?i)subagent|task', 40); time.sleep(2); step('subagent')
idle(120); step('after_subagent')
st.set(); r.kill()
