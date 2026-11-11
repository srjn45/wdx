# Claude Code and Codex each start in a worktree of their own on the toy
# repository, and TIDE shows both at work until each has committed.
# Run by record.sh, inside the demo it makes.
Output ../../public/blog/side-by-side/side-by-side.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type `wdx agent create --name notes-delete --worktree notes-delete --aca claude_code --prompt "Add a 'delete <n>' command to notes.py that removes note n, with a unittest in a new file test_delete.py, then commit it"`
Sleep 500ms
Enter
Sleep 2s
Type `wdx agent create --name notes-search --worktree notes-search --aca codex --prompt "Add a 'search <text>' command to notes.py that lists the notes containing the text, with a unittest in a new file test_search.py, then commit it"`
Sleep 500ms
Enter
Sleep 2s

# The worktree is a new folder: Claude Code asks whether to trust it, its
# cursor on "No, exit". Answer it in the agent's own screen, then detach.
Type "wdx agent attach --name notes-delete"
Enter
Wait+Screen@60s /trust this folder/
Sleep 2s
Down
Sleep 700ms
Enter
Sleep 1s
Ctrl+\
Type "d"
Sleep 1s

Type "wdx tide"
Enter
Sleep 2s
Type "notes"
Sleep 400ms
# Claude Code's status line says how much of the account's weekly limit is
# used, and where it resets: it is never filmed. TIDE shows Codex instead,
# the second agent of the list, picked out of frame.
Hide
Enter
Sleep 1s
Down
Sleep 300ms
Enter
Sleep 1s
Show
Sleep 6s
# Each worktree's row says ↑1 once its agent has committed.
Wait+Screen@300s /(?s)↑1.*↑1/
Sleep 4s
# Close Codex's tab, so that the next TIDE opens on the list, and quit.
Hide
Ctrl+q
Sleep 500ms
Type "-"
Sleep 500ms
Ctrl+q
Sleep 500ms
Type "q"
Sleep 1s
Show
Type "wdx worktree list"
Enter
Sleep 5s
