import sys,time
from rec import *
name=sys.argv[1]; env=sys.argv[2] if len(sys.argv)>2 else ''
r=Rec(name,env=env,cols=int(os.environ.get('RC','120')),rows=int(os.environ.get('RR','40')))
r.trust(); time.sleep(2); r.mark('idle'); time.sleep(2); r.snap('idle_nothing_running')
r.mark('turn_start')
r.say("In one message make two tool calls. 1: Bash with run_in_background true: ping -c 100 127.0.0.1 . 2: Agent with run_in_background true, subagent_type general-purpose, prompt: Run ping -c 100 127.0.0.1 in the foreground with Bash, then say done. Then end your turn with one word.")
end=time.time()+120; approvals=0; quiet=None
while time.time()<end:
    b=r.bottom(8)
    if 'Esc to cancel' in b:
        approvals+=1; r.snap(f'start_approval_{approvals}'); r.keys('Enter'); quiet=None; time.sleep(1.5); continue
    if '1 shell' in b and '↓ to manage' in b and 'esc to interrupt' not in b and '◯' in b:
        quiet=quiet or time.time()
        if time.time()-quiet>8: break
    else: quiet=None
    time.sleep(.2)
else: print('never reached both running',flush=True)
r.mark('both_running'); r.snap('both_running_idle_box')
r.mark('turn_to_permission'); r.say("Run this exact command with Bash: touch a.txt")
if r.wait('Esc to cancel',45,where=3):
    r.mark('permission'); time.sleep(3); r.snap('permission_while_bg'); r.keys('Escape'); r.mark('after_deny'); time.sleep(4); r.snap('after_deny_while_bg')
r.mark('turn_to_decision'); r.say("Use AskUserQuestion once: red or blue? Nothing else.")
if r.wait('Enter to select',45,where=3):
    r.mark('decision'); time.sleep(3); r.snap('decision_while_bg'); r.keys('Enter'); time.sleep(1.5); r.snap('decision_answered')
    if 'Submit' in r.bottom(10): r.keys('Enter')
r.turn_over(45); r.mark('after_decision'); time.sleep(2); r.snap('after_decision')
r.mark('wait_bg_end')
end=time.time()+130
while time.time()<end:
    b=r.bottom(8)
    if 'shell' not in b and '◯' not in b and 'esc to interrupt' not in b and 'Esc to cancel' not in b: break
    if 'Esc to cancel' in b: r.keys('Escape')
    time.sleep(.3)
r.mark('bg_ended'); time.sleep(4); r.snap('bg_ended'); r.turn_over(45); r.mark('idle_end'); time.sleep(5); r.snap('idle_end')
r.end()
