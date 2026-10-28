# Changelog

Every release of wdx, generated from the commits by git-cliff: do not edit.

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

