# Failure 2: The Stepped-On Source Tree (Clashing Edits)
# Multiple agents running in the same checkout clobber files and dirty the git index.
# In wdx, each agent gets an isolated git worktree with its own directory and branch.

Output ../../public/blog/four-failures/clashing-edits.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx agent list"
Enter
Sleep 2s

Type "wdx worktree list"
Enter
Sleep 2s

Type "ls -d .worktrees/*"
Enter
Sleep 2s

Type "wdx worktree diff --name notes-delete"
Enter
Sleep 2s

Type "wdx worktree diff --name notes-search"
Enter
Sleep 2s

# Verify that the main branch working tree remains completely clean
Type "git status"
Enter
Sleep 3s
