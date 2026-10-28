# The wdx daemon

Every `wdx` command that touches projects is served by the daemon: one process per home (`~/.wdx`) that holds the database and runs git. The CLI talks to it over the Unix socket `~/.wdx/wdx.sock`; its API is `api/openapi.yaml`.

```
wdx daemon start      start it (through the service when installed) and wait until it answers
wdx daemon stop       stop it, letting requests in flight finish
wdx daemon restart    after updating wdx by hand or changing ~/.wdx/config.yaml
wdx daemon status     running or not, pid, version, uptime, socket, service manager
```

Commands never start the daemon themselves: without it they fail with `daemon_unavailable` (exit 3) and the hint to run `wdx daemon start`. A daemon of another version than the CLI gets a warning; `wdx daemon restart` fixes it.

## As a service

`scripts/install.sh` installs the daemon as a service, and every install restarts it on the new binary:

| | Linux | macOS |
|---|---|---|
| Service | systemd user unit `~/.config/systemd/user/wdx.service` | launchd agent `~/Library/LaunchAgents/com.github.srjn45.wdx.plist` |
| Runs from | boot, with linger enabled (`loginctl enable-linger`) | login |
| Restarted | when it fails | when it fails |
| Its own output | `journalctl --user -u wdx.service` | `~/Library/Logs/wdx.log` |

`wdx daemon start` and `stop` go through the service manager when the service is installed and the home is the default `~/.wdx`; with another `WDX_HOME` they manage the process directly. A daemon stopped with `wdx daemon stop` stays stopped until `wdx daemon start` or the next boot or login.

Without a systemd user session (containers, some WSL setups), the installer starts the daemon directly; start it again after a reboot.

`scripts/install.sh --uninstall` stops and removes the service and the binary. `~/.wdx` (settings, database, logs, projects) stays, and so does linger, which other services may need.

## Shell completion

The installer also installs the completion of every shell it finds, so Tab completes commands, flags and their values: project names and ids from the daemon (only open ones for `wdx project close`), directories for `--local` and `--path`, and the help topics after `wdx help`.

| Shell | File | Loaded by |
|---|---|---|
| bash | `~/.local/share/bash-completion/completions/wdx` | the bash-completion package; without it the installer prints the `source <(wdx completion bash)` line for `~/.bashrc` |
| zsh | Homebrew's `share/zsh/site-functions/_wdx` when it is writable, else `~/.zsh/completions/_wdx` | zsh, from its `fpath`; when the directory is not in it the installer prints the `fpath=(...)` line for `~/.zshrc` |
| fish | `~/.config/fish/completions/wdx.fish` | fish |

The installer never edits shell startup files. `scripts/install.sh --completions` installs them again for the installed `wdx`, say after installing a new shell; `--uninstall` removes them. The scripts ask `wdx` for the candidates on every Tab, so they keep up with new commands without reinstalling. `wdx completion bash|zsh|fish` prints a script to load by hand.

A completion waits at most half a second for the daemon and offers nothing when it does not answer. It is logged at debug level in both processes (the CLI sends `Wdx-Log-Level: debug`), so Tab presses do not crowd the log.

Suggestions shown in grey as you type come from the shell: fish has them built in, zsh with the zsh-autosuggestions plugin. They draw on these completions with nothing more to set up.

## Gotchas

- **Environment.** The service does not run from your shell, so it does not see your shell's variables. The installer gives it the `PATH` you installed with, so it finds the same git; reinstall after moving git.
- **git over SSH.** On Linux the service has no `SSH_AUTH_SOCK`, so cloning `git@...` URLs with keys held by an agent fails (`clone_failed`). Use HTTPS with a credential helper, a key without a passphrase, or give the service your agent's socket:

  ```
  systemctl --user edit wdx.service
  # [Service]
  # Environment=SSH_AUTH_SOCK=%t/ssh-agent.socket
  wdx daemon restart
  ```

  On macOS the system ssh-agent's socket usually reaches launchd agents; a third-party agent may need the same treatment in the plist's EnvironmentVariables.
- **Settings** are read when the daemon starts: run `wdx daemon restart` after changing `~/.wdx/config.yaml`.
- **Logs.** The CLI and the daemon log to the same daily files in `~/.wdx/logs`; the `debug_id` an error prints finds the lines of both.
