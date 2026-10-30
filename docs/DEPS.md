# wdx deps: what wdx needs besides itself

Status: implemented, steps 1 to 5 (see Progress). Built in the worktree `.worktrees/deps`, branch `deps`, rebased onto `main` to land.

`wdx deps list` shows the programs wdx uses once it is installed, whether each is ready, and which features it enables; `wdx deps install --name <dependency>` installs one, on Ubuntu (and Debian) and macOS for now. A missing dependency drops only the features that need it: the rest of wdx keeps working.

## Decisions

Agreed with the human:

- **Only what wdx needs after it is installed.** What it takes to install and run wdx at all is not listed: a shell, systemd or launchd, the network, and `curl`, `tar` and `sha256sum` (checked by `install.sh` before wdx exists).
- **Each dependency is mandatory or optional.** A mandatory one enables wdx's core; an optional one a feature. A missing one drops its features only: the daemon still starts, and each feature that needs it fails with `dependency_missing`, naming the dependency and with the hint to install it.
- **Each dependency lists the features it enables**, shown by `wdx deps list` as on or off.
- **One dependency at a time.** `--name` is required: nothing is installed that the user did not name.
- **Installing that needs root asks, then runs `sudo` itself.** In a terminal, wdx shows the exact commands and asks; on yes it runs them, and `sudo` asks for the password itself. Only when that cannot work does wdx hand the commands over instead: without a terminal (or with `--json`) and a `sudo` that would need a password (`sudo -n true` fails), or no `sudo` at all. `--yes` answers the question in advance; with `--json` it is required, and then wdx runs `sudo -n`, which works only when no password is needed.
- **Every failure gives the way to do it by hand.** Whatever stopped the install (a refused password, the package manager failing, an unsupported system), wdx says why, with the package manager's own message, and then prints the commands or instructions to install it yourself.
- **docker and podman are installed too, when named.** Each comes with what it needs afterwards, done or said: docker's group (`usermod -aG docker`), after which the user logs in again; podman's machine on macOS (`podman machine init`, `podman machine start`). Either satisfies the `container` dependency.
- **Distributions:** Ubuntu and Debian (`apt`) and macOS (`brew`) first; Fedora, Arch and others are a column each later. Elsewhere `deps list` works and `deps install` says the system is not supported yet. On macOS without brew, wdx says so and shows brew's own install command; it never installs brew.

## The dependencies

| Name | Mandatory | Enables | Ready when | Ubuntu, Debian | macOS |
|---|---|---|---|---|---|
| `git` | yes | projects (create, open, clone, worktrees), and so terminals | `git` 2.31 or later is on the PATH (`rev-parse --path-format=absolute`) | `sudo apt-get install -y git` | `brew install git` |
| `clipboard` | no | tide's copy on release | Wayland: `wl-copy`; X11: `xclip` or `xsel`; macOS: `pbcopy`, always there | `wl-clipboard` on Wayland, `xclip` on X11 | none needed |
| `container` (installed as `docker` or `podman`) | no | `wdx observe` | `docker` or `podman` is installed and answers (`info`) | `docker`: `sudo apt-get install -y docker.io`, `sudo usermod -aG docker $USER`, then log in again; `podman`: `sudo apt-get install -y podman` | `docker`: `brew install --cask docker`, then open Docker once; `podman`: `brew install podman`, `podman machine init`, `podman machine start` |

Later, with the agents: each agent's CLI (`claude`, `codex`, …), optional, enabling its agent, ready when installed and logged in.

Not listed: git's identity (`user.name`, `user.email`): wdx never commits today; the agents will, and their dependency checks it. Credentials for private clones: they cannot be checked without reaching the host; a failed clone says what went wrong instead.

## The commands

```
wdx deps list                             every dependency: mandatory or not, its features, status, version, what to do
wdx deps install --name clipboard         installs it, asking before running sudo
wdx deps install --name docker --yes      without asking
wdx deps install --name git --json --yes  from a script: works when sudo needs no password
```

`wdx deps list --json`:

```json
{
  "system": {"os": "linux", "distribution": "ubuntu", "package_manager": "apt", "session": "wayland"},
  "dependencies": [
    {"name": "git", "mandatory": true, "features": ["projects", "terminals"],
     "status": "ready", "program": "git", "path": "/usr/bin/git", "version": "2.43.0", "detail": null, "install": null},
    {"name": "clipboard", "mandatory": false, "features": ["tide copy"],
     "status": "missing", "program": null, "path": null, "version": null, "detail": "wl-copy is not installed",
     "install": {"commands": [["sudo", "apt-get", "install", "-y", "wl-clipboard"]], "needs_root": true, "instructions": null}}
  ]
}
```

- `status`: `ready`, `missing`, `too_old` (git before 2.31), `not_ready` (installed, not usable: docker's daemon unreachable or denied).
- `install --name` takes `git`, `clipboard`, `docker` or `podman`. A dependency already ready is left alone: result `ready`.
- `install` results: `installed`, `ready`. Errors: `usage`, `cancelled` (the prompt answered no), `unsupported_system`, `install_failed`, `privileges_required` (root needed and `sudo` cannot ask). Every error carries the commands or instructions to do it by hand in `details.install` (as in `list`), which text mode prints after the reason; `install_failed` also carries the package manager's message.
- The session (Wayland or X11) comes from `XDG_SESSION_TYPE`, `WAYLAND_DISPLAY` and `DISPLAY`: `main` reads them into `Env`, as it does `NO_COLOR`. The distribution comes from `/etc/os-release`.
- The check runs in the CLI, on the machine the user is on. git is also run by the daemon, which may have another `PATH` as a service: saying when the daemon would not find it comes with step 4, where the daemon reports a missing git.

## Where it shows elsewhere

- The domain returns a typed error when a program is not found; the daemon maps it to `dependency_missing` (exit 3) with `details.dependency`; the cli adds the hint `run wdx deps install --name <dependency>`.
- tide: a copy that no clipboard tool could make says so on the status line, `not copied: wdx deps install --name clipboard`.
- `wdx observe` keeps its `runtime_missing`, its hint pointing at `wdx deps`.

## Steps

1. **`docs:`** this file.
2. **`feat:` `wdx deps list`**: the table of dependencies, the system detection, the checks, text and `--json`.
3. **`feat:` `wdx deps install --name`**: the install plans per package manager and dependency, docker's and podman's steps after, the prompt and `--yes`, running `sudo` or handing over the commands, the reason and the commands on every failure; tests replace the command runner, so nothing is installed.
4. **`feat:` features drop, not wdx**: `dependency_missing` for git in the daemon, tide's copy notice, observe's hint.
5. **`docs:`** this file marked implemented; the docs of the commands it touched.

## Progress

- Step 1: committed (`fbb1c5c`).
- Step 2: committed (`223f487`). `internal/deps` holds the table (`Dependencies`), `Detect` (the OS; on Linux the distribution from `/etc/os-release`, `apt` for Ubuntu, Debian and those `ID_LIKE` them; on macOS `brew` when installed) and the checks; a `Probe` gives them `LookPath`, a program's output and `ReadFile`, which tests replace. `main` reads the session into `Env.Deps.Session` (`XDG_SESSION_TYPE`, `WAYLAND_DISPLAY`, `DISPLAY`). Each dependency in the JSON also has `program` (the one found, as `wl-copy` or `podman`) and `detail` (why it is not ready). docker counts as ready only when `docker info` answers; when it does not, the line saying why (permission denied, the daemon not running) is the detail. Each check is logged (`dependency checked`). 
- Step 3: committed (`73500ee`). `deps.PlanFor` gives the commands for a name and system, what is left after (docker's new login, opening Docker on macOS) or, as a `*UnsupportedError`, the instructions (another distribution; macOS without brew; Linux without a graphical session, for the clipboard). apt refreshes its index first (`apt-get update`). In a terminal the commands that need root are shown and asked about (`[y/N]`, logged as the answer to "install with sudo"), then run with the terminal, so `sudo` asks for the password and the package manager's output shows as it goes; without a terminal they run as `sudo -n`, after `sudo -n true` says no password is needed. Every error is a problem whose hint is the way to do it by hand ("install it yourself: <commands joined by &&>", or the instructions) and whose `details.install` is the plan; `install_failed` keeps the last 20 lines of output in `details.output`. A name already ready is left alone (`ready`); docker or podman named is checked as that program. `deps list` gives the plan of each dependency not ready under `install` (docker's for `container`), and in text mode the command that installs it: for `container`, `--name docker` or `--name podman`, the user's choice on either system. New codes, reported by the CLI only: `cancelled`, `unsupported_system`, `install_failed`, `privileges_required`.
- Step 4: committed (`8b82d98`). `git` returns a `*git.MissingError` when the program is not found (in `run` and `Clone`); the daemon maps it to `dependency_missing` (503, exit 3) with `details.dependency: "git"`, and the cli adds the hint `run wdx deps install --name <dependency>` to any `dependency_missing` problem. The project commands that run git (create, open, get) list the code. tide says on the status line, until the next key or click, "no clipboard tool, the copy may be lost: wdx deps install --name clipboard" when no tool took a copy (OSC 52 may have; no terminal tells). `wdx observe`'s `runtime_missing` hints at `wdx deps install --name docker, or --name podman`. Dropped from the design: `deps list` reporting the daemon's own git; `dependency_missing` now says it when a project needs git, without a new field in the API.
- Step 5: this file marked implemented; the README says what wdx needs and points at `wdx deps`; docs/V1_FEATURES.md ticks installing missing dependencies.

## What changed on the way

- `--name` became required: nothing is installed that was not named, and there is no install-all.
- docker and podman are installed too, when named, on either system: the user picks; `deps list` offers both for `container`.
- Running `sudo` comes first; the commands are handed over only when `sudo` cannot ask (no terminal and a password needed).
- Every failure carries the way to do it by hand, as a hint to paste and as `details.install`.
- `deps list` does not report the daemon's own git: `dependency_missing` says it when a project needs git.
- Each dependency in the JSON also has `program` and `detail`, and `install` for those not ready.
- Left for later: Fedora (`dnf`), Arch (`pacman`) and other distributions; the agents' CLIs with their login checks; tide choosing its clipboard tool by the session as `deps` does.

