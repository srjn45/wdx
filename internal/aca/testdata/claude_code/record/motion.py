import sys,time
from rec import *
name,cols,rows,mode=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]),sys.argv[4]; env=sys.argv[5] if len(sys.argv)>5 else ''; extra=sys.argv[6] if len(sys.argv)>6 else ''
r=Rec(name,cols=cols,rows=rows,args=f"--safe-mode --model haiku --permission-mode {mode} {extra}",env=env)
r.trust(); r.mark('idle'); time.sleep(4); r.snap('idle_start')
r.mark('turn_subagent'); r.say("Agent with run_in_background true, subagent_type general-purpose, prompt: Run ping -c 15 127.0.0.1 with Bash, then say done. Then end your turn with one word.")
time.sleep(5); r.settle(still=5,limit=8) if False else None
end=time.time()+40
while time.time()<end:
    b=r.nb()
    if any('Esc to cancel' in x or x.strip()=='to cancel' for x in b[-3:]): r.snap('sub_approval'); r.keys('Enter'); time.sleep(1.5); continue
    if any('◯' in x for x in b[-3:]): r.mark('subagent_running'); time.sleep(3); r.snap('subagent_running'); break
    time.sleep(.2)
r.settle(still=6,limit=60); r.mark('idle_tasks'); time.sleep(5); r.snap('idle_tasks')
r.mark('typing_at_idle'); r.slow('hello there'); r.mark('draft_sits'); time.sleep(9); r.snap('draft_at_idle'); r.mark('clear_draft'); r.keys('C-u'); r.mark('idle_after_clear'); time.sleep(7); r.snap('idle_after_clear')
r.mark('turn_story'); r.say("Write a 250 word story about a lighthouse. No tools.")
time.sleep(4); r.snap('working_cut_off'); r.mark('typing_in_turn'); r.slow('abc',.3); time.sleep(1); r.snap('typing_in_turn'); r.keys('C-u'); r.mark('turn_story_on'); r.settle(still=5,limit=60); r.mark('idle_after_story'); time.sleep(4); r.snap('idle_after_story')
r.mark('turn_draft'); r.say("Write a 150 word poem about tides. No tools.")
time.sleep(2.5); r.mark('draft_in_turn'); r.slow('draft',.2); r.snap('draft_in_turn'); r.settle(still=5,limit=60,approve=False); r.mark('draft_after_turn'); time.sleep(7); r.snap('draft_after_turn'); r.keys('C-u'); r.mark('cleared'); time.sleep(5)
r.mark('turn_silent_tool'); r.say("Run with Bash in the foreground, no background: ping -q -c 12 127.0.0.1")
r.settle(still=6,limit=45); r.mark('idle_end'); time.sleep(4); r.snap('idle_end')
r.end()
