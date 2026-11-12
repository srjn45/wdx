# Running a plan: breaks a feature into tasks, runs them in slots,
# hands off summaries, and lands cleanly on plan/release.
Output ../../public/blog/plans/plan-run.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "cat release.yaml"
Enter
Sleep 3s

Type "wdx plan create --name release --path release.yaml"
Enter
Sleep 2s

Type "wdx plan status --name release"
Enter
Sleep 3s

Type "wdx plan run --name release"
Enter
Sleep 2s

Type "wdx plan status --name release"
Enter
Sleep 4s

# Wait until contributing lands
Hide
Type "while ! wdx plan status --name release 2>/dev/null | grep -q 'contributing.*done'; do sleep 1; done"
Enter
Sleep 500ms
Show

Type "wdx plan status --name release"
Enter
Sleep 4s

# Wait until license lands
Hide
Type "while ! wdx plan status --name release 2>/dev/null | grep -q 'license.*done'; do sleep 1; done"
Enter
Sleep 500ms
Show

Type "wdx plan status --name release"
Enter
Sleep 3s

Type "git log --oneline plan/release"
Enter
Sleep 4s
