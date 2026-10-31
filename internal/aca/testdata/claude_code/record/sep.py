import sys,time
from rec import *
name=sys.argv[1]
r=Rec(name,args='--safe-mode --model haiku --permission-mode auto',cols=int(os.environ.get('RC','120')),rows=int(os.environ.get('RR','40')),env=os.environ.get('RE',''))
r.trust(); time.sleep(2); r.mark('idle_nothing_running'); time.sleep(3); r.snap('idle_nothing_running')
r.mark('turn_bg_shell'); r.say("Bash with run_in_background true: ping -c 30 127.0.0.1 . Then end your turn with one word.")
r.turn_over(); r.mark('bg_shell_after_turn'); time.sleep(5); r.snap('bg_shell_after_turn')
r.mark('bg_shell_to_end'); time.sleep(32); r.snap('bg_shell_ended'); r.turn_over(45); r.mark('after_bg_shell'); time.sleep(3); r.snap('after_bg_shell')
r.mark('turn_bg_subagent'); r.say("Agent with run_in_background true, subagent_type general-purpose, prompt: Run ping -c 30 127.0.0.1 in the foreground with Bash, then say done. Then end your turn with one word.")
r.turn_over(); r.mark('bg_subagent_after_turn'); time.sleep(5); r.snap('bg_subagent_after_turn')
r.mark('bg_subagent_to_end'); time.sleep(35); r.snap('bg_subagent_ended'); r.turn_over(45); r.mark('after_bg_subagent'); time.sleep(3); r.snap('after_bg_subagent')
r.end()
