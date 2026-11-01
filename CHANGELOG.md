# Changelog

Every release of wdx, generated from the commits by git-cliff: do not edit.

## v0.2.0 - 2026-11-01

### Breaking changes

- add the terminal commands and stop terminals on project close
- run wdx terminal attach full-window, as tmux does
- make update --rollback --json say whether to restore the backup
- page terminal history by from and count, or by tail
- answer a request to an unknown endpoint with 400
- create agents under other agents, shown as a tree
- show and change settings with wdx setting

### Features

- update wdx in place and roll it back
- add the terminals table and the terminal.shell setting
- run a terminal's shell in a holder process that outlives the daemon
- supervise terminals' holders and keep their state
- serve terminals over the API and stop them with their project
- keep terminals running when the service stops or restarts
- publish a GitHub Release for every pushed tag
- give terminals' shells WDX_HOME so wdx run in them reaches their daemon
- list a project's terminals in project get and add terminal get
- write the config file from the settings, and anew on every update
- let the daemon drive holders of protocol version 2
- keep a terminal's screen in a headless vt for attach
- keep a terminal's output in a spool and its history in files
- let clients attach to a terminal's holder
- add a client that attaches to a terminal's holder
- serve a terminal's history and delete what removed terminals kept
- add wdx terminal attach and wdx terminal history
- add a Bubble Tea pane that shows an attached terminal
- scroll an attached terminal's pane back through its whole history
- color attach's status line by its state
- show an attached pane at the size another client set
- say what did not happen on every changing command's error
- give a terminal's updated_at, when its state last changed
- add wdx tide, the terminal IDE's layout with placeholder panes
- let tide list the projects from the daemon at start
- open closed projects in tide from [Open Project]
- switch projects in tide with the switcher and Alt+Shift+arrows
- frame tide's popups with a title, a body and their keys
- open a local repository in tide from a directory browser
- clone and create projects in tide, answering their questions
- select text inside one tide pane by dragging, copied on release
- stream a project from the daemon, whole on every change
- show the project's terminals as tide's tabs, followed live
- create a terminal with C-q t and remove one with C-q x in tide
- log when a terminal's vt falls behind its output and catches up
- list what wdx needs besides itself with wdx deps list
- install a dependency with wdx deps install --name
- drop only the features of a missing dependency
- run AI coding agents as terminals of a project
- show a terminal's history alone, read-only, in termview
- show the selected terminal live in tide's terminal pane
- list a project's agents in tide and show the active one live
- open and switch projects in framed popups in tide
- give tide's left pane a quarter of the width
- start an agent in a worktree, created when it is new
- save the dev channel when installing from it
- take the menu's letters in tide's project pane without C-q
- accept holders of protocol versions 3 and 4
- read an agent's activity and draft from its screen
- keep what an agent is doing in its holder
- send a message to a terminal with wdx terminal send
- send tide's own keys to the program in a pane
- give a running agent a message with wdx agent send
- show the version in the status bars and reload tide with C-q r
- show what an agent is doing in the API, wdx agent and tide
- show how many messages wait for an agent
- open agents in tabs in tide's right pane

### Fixes

- publish and update from the srjn45/wdx releases
- build with Go 1.26.10 for its standard library security fixes
- open terminals' PTYs without reading a stale PTY number
- keep attached clients' output flowing past the hello's write timeout
- number an attached pane's lines as the holder does after a vt reset
- answer hello without waiting for the holder's vt
- keep the holder's vt going when a program never reads its answers
- send attached clients the shell's exit before the holder quits
- fetch failed history again on the next scroll, not at once
- give the window back when wdx terminal attach gets a signal
- refuse ctrl-i and ctrl-m as attach's prefix key
- finish a change when the client that asked for it leaves
- never leave a clone cut short where a project's checkout goes
- cancel a change the stopping daemon gives up on, and wait for it
- let the daemon stop while a terminal's holder is being dialled again
- report a terminal removed with --stop-running as stopped
- stop a project's terminals together, not one after another
- stop a terminal's holder even when nothing watches it
- delete the sockets killed holders leave behind
- stop the daemon a failed update started before putting the old one back
- never lose the rollback point, nor fail an update that worked
- finish an update's switch when wdx is interrupted during it
- refuse a rollback once another install replaced the update
- always say a signal held during an update's switch
- stream a release's archive to disk instead of into memory
- let launchd wait for the daemon to stop
- attach shows the screen under less or vim, and the pen and line drawing
- keep out of history lines that never scrolled off the screen
- free what scrolls off the alternate screen
- retry starting the daemon while its binary is text file busy
- keep the last screen in history before telling of the shell's exit
- stop Claude Code's idle title from printing at its prompt
- keep all of a shell's output in history when the vt is behind at its exit
- select a terminal created in tide whichever comes first
- keep a clone that is cancelled once it is in place
- stop a terminal whose connection to its holder just dropped

## v0.1.0 - 2026-10-28

### Features

- scaffold wdx CLI with release build and installer
- add projects with open --local and close
- add project remove
- add project open --git
- add project create
- add flags to answer prompts without a terminal
- add --json output and exit codes
- support --json in project open
- support --json in project create
- add project list
- add project get
- add --help with output, errors and --json
- add structured JSON logging with debug_id
- load settings once into a config passed by value
- name projects by --id or --name in close and remove
- read settings from <home>/config.yaml
- add the daemon API spec and the daemon serving it
- serve every command through the daemon
- install and manage the daemon as a systemd or launchd service
- log the error code at the top level of returned lines
- send the daemon's request metrics over OTLP
- run Grafana with the logs and metrics via wdx observe
- color text output and split errors into hint and debug_id lines
- render help in our own layout, with examples and help topics
- complete commands, flags and projects in bash, zsh and fish
- tag releases on a stable and a dev channel

