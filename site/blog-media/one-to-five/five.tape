# Five agents on one screen: a parent with 3 children in worktrees,
# a Codex agent, answering a permission question in its tab, and the stats tab.
Output ../../public/blog/one-to-five/five.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx agent list"
Enter
Sleep 3s

Type "wdx tide"
Enter
Sleep 2s
Type "notes"
Sleep 400ms
Enter
Sleep 2s

# Navigate down to tests in the agent list
Down
Sleep 500ms
Down
Sleep 500ms
Down
Sleep 500ms
Down
Sleep 500ms

# Open tests with 'o' (open and switch to it)
Type "o"
Sleep 2s

# Answer the prompt: Down to 'Yes, I trust this folder' and Enter
Down
Sleep 700ms
Enter
Sleep 2s

# Open the stats tab: C-q S
Ctrl+q
Sleep 400ms
Type "S"
Sleep 5s

# Close the stats tab with -
Type "-"
Sleep 1500ms

# Quit TIDE: C-q q
Ctrl+q
Sleep 400ms
Type "q"
Sleep 1500ms

Type "wdx agent list"
Enter
Sleep 4s
