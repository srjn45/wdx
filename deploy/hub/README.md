# Running wdx-hub

`wdx-hub serve` serves three endpoints: the web endpoint for browsers (`listen.web`, `:8080`), the node endpoint for wdx daemons (`listen.nodes`, `:8443`, always TLS with the hub's own certificate authority) and the admin endpoint, a Unix socket `wdx-hub.sock` in its home. Its home holds the config file, the database, the certificate authority, the socket and the logs. `public_url` is required: the address browsers and nodes reach the hub at, `http` only for `localhost` and `127.0.0.1`. `serve` also needs `master_key`, 32 random bytes in base64 that seal the secrets the hub stores, such as its users' authenticator secrets; make one with `head -c 32 /dev/urandom | base64` and keep it safe, since those secrets are lost with it. `wdx-hub help settings` lists every setting; each can come from the environment as `WDX_HUB_<KEY>` (`listen.web` is `WDX_HUB_LISTEN_WEB`), which wins over the config file.

The other `wdx-hub` commands (`version`, `setting`, `backup`) talk to the running hub over the admin socket, so they run where the socket is, with the same home.

## With the install script

On Linux with systemd, amd64 or arm64:

```sh
curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install-hub.sh | sudo sh
```

It makes the system user `wdx-hub` with its home in `/var/lib/wdx-hub` (the hub's home is the default in it, `/var/lib/wdx-hub/.wdx-hub`), asks `public_url` and how the web endpoint gets its TLS (a proxy in front, or certificate files), makes the master key, installs the binary at `/usr/local/bin/wdx-hub` and the unit `wdx-hub service unit` prints at `/etc/systemd/system/wdx-hub.service`, starts it and prints an invite for the first user. Without a terminal it takes `WDX_HUB_PUBLIC_URL`, `WDX_HUB_WEB_TLS` (`off` or `files`), `WDX_HUB_WEB_TLS_CERT` and `WDX_HUB_WEB_TLS_KEY`; `WDX_CHANNEL`, `WDX_VERSION` and `WDX_DOWNLOAD_BASE` choose the release, as for wdx's `install.sh`.

Admin commands run with sudo, which makes `wdx-hub` run as its user: `sudo wdx-hub user invite`. `sudo wdx-hub update` shows whether a newer release is out, `sudo wdx-hub update --apply` installs it with a backup of the database, putting everything back when it does not start, and `sudo wdx-hub update --rollback` goes back. The script run again updates the installed hub the same way; `sh -s -- --uninstall` removes the service and the binary and keeps the user and its home, and `--purge` (with `--yes` when there is no terminal) removes them too.

The website's [Running a hub](https://srjn45.github.io/wdx/docs/guides/running-a-hub/) says it all at length, with a Caddy example for the proxy and the ports to open. Fly.io, the container and the unit by hand, below, are for those who want them; `wdx-hub update` updates only a hub the script installed.

## On Fly.io

`fly.toml` runs the hub on Fly.io, one machine always on, built by Fly's builder from `Dockerfile`: the web endpoint (`:8080`) behind Fly's proxy, which terminates TLS for `https://<name>.fly.dev`, the node endpoint on 8443 as raw TCP, which needs the app's dedicated IPv4, and the home on the volume `wdx_hub_data` at `/data`. It names no app and no region: every command takes `--app`, and the first deploy takes the region. A test keeps its ports, mount and health check in line with the hub's defaults.

After creating the app and its addresses, and setting `WDX_HUB_MASTER_KEY` and `WDX_HUB_PUBLIC_URL` as its secrets, deploy from a clean checkout of a release tag:

```sh
make hub-fly-deploy APP=<name> REGION=<region>
```

`REGION` is needed only at the first deploy, which creates the machine and its volume there; later, the machine stays where it is. `scripts/hub-fly-deploy.sh` snapshots the volume when the app has a machine, deploys the tag and waits until `https://<name>.fly.dev/v1/version` answers its version. Admin commands run as the image's user: `fly ssh console --app <name> --user nonroot -C "wdx-hub user invite"`. `wdx-hub update` does not apply on Fly: a new tag is deployed the same way, and an older image is deployed again to roll back. [Running a hub](https://srjn45.github.io/wdx/docs/guides/running-a-hub/#on-flyio) gives the first deploy step by step, the rollback across a migration through the snapshot, `trusted_proxies` and the cost.

## In a container

From the repository's root:

```sh
export WDX_HUB_MASTER_KEY=$(head -c 32 /dev/urandom | base64) # once; keep it
VERSION=$(git describe --tags | sed 's/^v//') COMMIT=$(git rev-parse --short HEAD) \
	docker compose -f deploy/hub/compose.yaml up --build -d
```

`compose.yaml` builds the image from `Dockerfile`, keeps the home (`/data`) in the named volume `hub-data` and gives the settings as environment, with `public_url` `http://localhost:8080` and `master_key` from `WDX_HUB_MASTER_KEY`, which must be the same at every start. The hub logs to stdout: `docker compose -f deploy/hub/compose.yaml logs hub`.

The image has no shell; the admin socket is reached by running `wdx-hub` in the container, which has `WDX_HUB_HOME=/data`:

```sh
docker compose -f deploy/hub/compose.yaml exec hub wdx-hub version
docker compose -f deploy/hub/compose.yaml exec hub wdx-hub setting set --name log.level --value debug
docker compose -f deploy/hub/compose.yaml exec hub wdx-hub setting list
docker compose -f deploy/hub/compose.yaml exec hub wdx-hub backup --out /data/backup.db
```

A setting given in `compose.yaml` wins over one `wdx-hub setting set` writes to the volume's config file; change it in `compose.yaml` and run `up -d` again.

## As a systemd service, by hand

`wdx-hub.service` is the unit the script installs, kept equal by a test to what `wdx-hub service unit` prints. It runs the hub as the system user `wdx-hub`, whose home is `/var/lib/wdx-hub`, so the hub's home is the default, `/var/lib/wdx-hub/.wdx-hub`. Make the user, install the binary from its release archive, `wdx-hub_<version>_linux_<arch>.tar.gz`, and the unit; set `public_url` and `master_key` as that user, then start it:

```sh
sudo useradd --system --user-group --home-dir /var/lib/wdx-hub --create-home --shell /usr/sbin/nologin wdx-hub
sudo chmod 0700 /var/lib/wdx-hub
sudo install -m 0755 wdx-hub /usr/local/bin/wdx-hub
sudo install -m 0644 deploy/hub/wdx-hub.service /etc/systemd/system/wdx-hub.service
sudo systemctl daemon-reload
sudo wdx-hub setting set --name public_url --value https://hub.example.com
head -c 32 /dev/urandom | base64 | sudo wdx-hub setting set --name master_key --value -
sudo systemctl enable --now wdx-hub
journalctl -u wdx-hub
sudo wdx-hub version
```

`sudo wdx-hub …` runs as the `wdx-hub` user once the binary and the unit are in place, as `sudo -u wdx-hub -H wdx-hub …` does anywhere. A web endpoint on a port below 1024 needs the capability to bind it: `wdx-hub service unit --privileged-port` prints the unit with it.
