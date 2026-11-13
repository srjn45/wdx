# Failure 4: The Blind Merge (Mystery Diffs)
# Merging agent changes blindly risks clobbered logic and broken builds on main.
# In wdx, inspect worktree diffs visually (o), then land with one key (L):
# automated tests run and changes rebase linearly onto main with zero merge commits.

Output ../../public/blog/four-failures/mystery-diffs.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx worktree list"
Enter
Sleep 2s

Type "wdx tide"
Enter
Sleep 2s
Type "notes"
Sleep 300ms
Enter
Sleep 2s

# Cursor starts at coordinator (row 0 of Agents).
# Move down past main worktree to notes-count worktree:
Down
Sleep 800ms

Down
Sleep 800ms

# Open notes-count diff and switch focus to it ('o'):
Type "o"
Sleep 2s

# In diff view: navigate down into the implementation:
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 600ms

# Trigger guarded landing with 'L':
Type "L"
Sleep 1s

# Confirm landing (y) - wdx runs automated checks and rebases on main:
Type "y"
Sleep 3s

# TIDE prompts to clean up the landed worktree:
Wait+Screen@15s /Remove/
Sleep 1s
Type "y"
Sleep 2s

# Quit TIDE: C-q q
Ctrl+q
Sleep 500ms
Type "q"
Sleep 1s

# Main branch has the commit cleanly rebased with no merge commit
Type "git log --oneline -n 2"
Enter
Sleep 3s
