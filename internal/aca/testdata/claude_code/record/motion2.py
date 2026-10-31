import sys,time
from rec import *
name,cols,rows,mode=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]),sys.argv[4]; env=sys.argv[5] if len(sys.argv)>5 else ''; extra=sys.argv[6] if len(sys.argv)>6 else ''
r=Rec(name,cols=cols,rows=rows,args=f"--safe-mode --model haiku --permission-mode {mode} {extra}",env=env)
SUB="Agent with run_in_background true, subagent_type general-purpose, prompt: Run ping -c 8 127.0.0.1 with Bash, then say done. Then end your turn with one word."
def sub(tag):
    r.mark('turn_'+tag); r.say(SUB); time.sleep(3); end=time.time()+30
    while time.time()<end:
        b=r.nb()
        if any('Esc to cancel' in x or x.strip()=='to cancel' for x in b[-3:]): r.keys('Enter'); time.sleep(1.5); continue
        if any('◯' in x for x in b[-3:]): r.mark(tag+'_running'); time.sleep(2); r.snap(tag+'_running'); break
        time.sleep(.2)
    r.settle(still=4,limit=40); r.mark(tag+'_ended'); r.snap(tag+'_ended')
r.trust(); r.mark('idle'); time.sleep(3)
sub('sub1')
r.mark('turn_story'); r.say("Write a 250 word story about a lighthouse. No tools.")
time.sleep(4); r.snap('working_cut_off'); r.settle(still=5,limit=60); r.mark('idle_after_story'); time.sleep(5); r.snap('idle_after_story')
sub('sub2')
def tool(tag,n):
    r.mark('turn_'+tag); r.say(f"Run with Bash in the foreground, no background: ping -q -c {n} 127.0.0.1 . Then say done.")
    end=time.time()+8
    while time.time()<end:
        if any('Esc to cancel' in x or x.strip()=='to cancel' for x in r.nb()[-3:]): r.keys('Enter'); time.sleep(.5); break
        time.sleep(.1)
    r.wait('ping -q',10,where=20); r.mark(tag+'_running')
tool('typed',16); time.sleep(3); r.mark('typing_in_turn'); r.slow('abc',.3); time.sleep(1); r.snap('typing_in_turn'); r.keys('C-u'); r.mark('cleared_in_turn'); time.sleep(1.5); r.mark('turn_on'); r.settle(still=5,limit=40); r.mark('idle_after_typed'); time.sleep(3); r.snap('idle_after_typed')
sub('sub3')
tool('draft',9); time.sleep(2); r.mark('draft_in_turn'); r.slow('draft',.2); r.snap('draft_in_turn'); r.settle(still=6,limit=40,approve=False); r.mark('draft_after_turn'); time.sleep(6); r.snap('draft_after_turn'); r.keys('C-u'); r.mark('cleared'); time.sleep(5); r.snap('idle_end')
r.end()
