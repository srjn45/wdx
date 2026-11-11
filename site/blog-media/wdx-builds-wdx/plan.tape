# A plan of three tasks: two run at once, the third waits for both. wdx runs
# an agent for each task that is ready and lands its work on plan/extras.
# Run by record.sh, after children.tape, in the same demo.
Output ../../public/blog/wdx-builds-wdx/plan.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "cat extras.yaml"
Enter
Sleep 4s
Type "wdx plan create --name extras --path extras.yaml"
Enter
Sleep 2s
Type "wdx plan run --name extras"
Enter
Sleep 3s
Type "clear; wdx plan status --name extras"
Enter
Sleep 4s

Type "wdx tide"
Enter
Sleep 2s
Type "/extras"
Sleep 500ms
Enter
Sleep 300ms
Enter
Sleep 3s
Wait+Screen@600s /3\/3 task/
Sleep 4s
Ctrl+q
Sleep 800ms
Type "q"
Sleep 1s
Type "clear; wdx plan status --name extras"
Enter
Sleep 6s
