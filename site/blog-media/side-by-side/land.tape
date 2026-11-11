# Both worktrees land on main, one after the other: the first fast-forwards;
# the second's rebase stops on a conflict, which wdx undoes; TIDE asks Codex
# to rebase it, and it lands. Run by record.sh after side-by-side.tape, in the
# same demo.
Output ../../public/blog/side-by-side/land.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx worktree list"
Enter
Sleep 3s
Type "wdx worktree diff --name notes-delete | head -n 22"
Enter
Sleep 6s
Type "clear"
Enter
Type `wdx project landing --name notes --check "python3 -m unittest"`
Enter
Sleep 2s
Type "wdx worktree land --name notes-delete"
Enter
Wait+Screen@180s /landed worktree notes-delete/
Sleep 2s
Type "git log --oneline"
Enter
Sleep 3s
Type "wdx worktree land --name notes-search"
Enter
Wait+Screen@180s /debug_id/
Sleep 7s

Type "wdx tide"
Enter
Sleep 2s
# To the top of the list, then to notes-search's worktree, and into its tab.
Up@50ms 10
Sleep 500ms
Down@400ms 4
Sleep 1s
Type "o"
Sleep 4s
Type "L"
Sleep 5s
Enter
Wait+Screen@60s /notes-search\s+working/
Sleep 3s
Wait+Screen@300s /notes-search\s+\d+k/
Wait+Screen@60s /change\s+.\s+search/
Sleep 4s
Type "L"
Sleep 4s
Type "y"
Wait+Screen@120s /landed notes-search|Remove/
Sleep 5s
Escape
Sleep 1s
Ctrl+q
Sleep 800ms
Type "q"
Sleep 1s
Type "git log --oneline --graph"
Enter
Sleep 6s
