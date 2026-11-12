# Handling a failed landing: check failure diagnostic, inspecting the task,
# resolving in the slot, and landing again.
Output ../../public/blog/plans/plan-retry.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx plan status --name fail-test"
Enter
Sleep 3s

Type "wdx plan task get --plan fail-test --task add-fail"
Enter
Sleep 4s

Type "cd .worktrees/fail-test-1"
Enter
Sleep 1500ms

Type "echo 'Clean note' > notes.txt && git commit -am 'fix: clean note'"
Enter
Sleep 2500ms

Type "cd ../.."
Enter
Sleep 1s

Type "wdx plan task land --plan fail-test --task add-fail"
Enter
Sleep 2s

# Wait until task completes landing
Hide
Type "while ! wdx plan status --name fail-test 2>/dev/null | grep -q 'add-fail.*done'; do sleep 1; done"
Enter
Sleep 500ms
Show

Type "wdx plan status --name fail-test"
Enter
Sleep 4s
