import sys,time
from rec import *
name=sys.argv[1]; env=sys.argv[2] if len(sys.argv)>2 else ''
r=Rec(name,env=env,cols=int(os.environ.get('RC','120')),rows=int(os.environ.get('RR','40')))
r.trust(); time.sleep(2); r.mark('idle'); time.sleep(1)
r.mark('turn_two_shells'); r.say("In one message, two Bash calls, each run_in_background true: ping -c 40 127.0.0.1 and ping -c 41 127.0.0.1 . Then end your turn with one word.")
end=time.time()+60; a=0
while time.time()<end:
    b=r.bottom(6)
    if 'Esc to cancel' in b: a+=1; r.snap(f'approval_{a}'); r.keys('Enter'); time.sleep(1.5); continue
    if 'shell' in b and 'esc to interrupt' not in b and '❯' in b: break
    time.sleep(.2)
r.mark('two_shells'); time.sleep(3); r.snap('two_shells')
r.mark('wait_end'); time.sleep(42); r.snap('shells_ended'); r.turn_over(40); r.mark('idle_after_shells'); time.sleep(3); r.snap('idle_after_shells')
for t in (1,2):
    r.mark(f'turn_fg_{t}'); r.say("Call the Agent tool, subagent_type general-purpose, with run_in_background false, and wait for it. Prompt: Run ping -c 12 127.0.0.1 with Bash, then say done.")
    end=time.time()+50; a=0; fg=False
    while time.time()<end:
        b=r.bottom(6); s=r.screen()
        if 'Esc to cancel' in b: a+=1; r.snap(f'fg{t}_approval_{a}'); r.keys('Enter'); time.sleep(2); continue
        if 'Backgrounded agent' in s and 'turn_fg' in r.label: r.snap(f'fg{t}_backgrounded'); r.label='fg_was_backgrounded'
        if 'esc to interrupt' not in b and a>0: break
        if 'esc to interrupt' in b and a>0 and not fg: fg=True; time.sleep(3); r.snap(f'fg{t}_running')
        time.sleep(.2)
    r.turn_over(40); r.mark('idle_after_fg'); time.sleep(2); r.snap(f'fg{t}_after')
    if 'Backgrounded agent' not in r.screen(): break
    time.sleep(15)
r.end()
