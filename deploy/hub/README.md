# Running wdx-hub

`wdx-hub serve` serves three endpoints: the web endpoint for browsers (`listen.web`, `:8080`), the node endpoint for wdx daemons (`listen.nodes`, `:8443`, always TLS with the hub's own certificate authority) and the admin endpoint, a Unix socket `wdx-hub.sock` in its home. Its home holds the config file, the database, the certificate authority, the socket and the logs. `public_url` is required: the address browsers and nodes reach the hub at, `http` only for `localhost` and `127.0.0.1`. `wdx-hub help settings` lists every setting; each can come from the environment as `WDX_HUB_<KEY>` (`listen.web` is `WDX_HUB_LISTEN_WEB`), which wins over the config file.

The other `wdx-hub` commands (`version`, `setting`, `backup`) talk to the running hub over the admin socket, so they run where the socket is, with the same home.

## In a container

From the repository's root:

```sh
VERSION=$(git describe --tags | sed 's/^v//') COMMIT=$(git rev-parse --short HEAD) \
	docker compose -f deploy/hub/compose.yaml up --build -d
```

`compose.yaml` builds the image from `Dockerfile`, keeps the home (`/data`) in the named volume `hub-data` and gives the settings as environment, with `public_url` `http://localhost:8080`. The hub logs to stdout: `docker compose -f deploy/hub/compose.yaml logs hub`.

The image has no shell; the admin socket is reached by running `wdx-hub` in the container, which has `WDX_HUB_HOME=/data`:

```sh
docker compose -f deploy/hub/compose.yaml exec hub wdx-hub version
docker compose -f deploy/hub/compose.yaml exec hub wdx-hub setting set --name log.level --value debug
docker compose -f deploy/hub/compose.yaml exec hub wdx-hub setting list
docker compose -f deploy/hub/compose.yaml exec hub wdx-hub backup --out /data/backup.db
```

A setting given in `compose.yaml` wins over one `wdx-hub setting set` writes to the volume's config file; change it in `compose.yaml` and run `up -d` again.

## As a systemd service

The unit runs the hub as a user systemd allocates for it (`DynamicUser=yes`), its home in `/var/lib/wdx-hub` (`StateDirectory=wdx-hub`), which only that user can reach. So `wdx-hub`'s other commands run as that same user, in a unit of their own with the same `User=`, which systemd gives the same user while the service runs:

```sh
hub() {
	sudo systemd-run --pipe --wait --quiet \
		-p DynamicUser=yes -p User=wdx-hub -p StateDirectory=wdx-hub \
		-p Environment=WDX_HUB_HOME=/var/lib/wdx-hub \
		/usr/local/bin/wdx-hub "$@"
}
```

Install the binary from its release archive, `wdx-hub_<version>_linux_<arch>.tar.gz`, and the unit; set `public_url`, then start it:

```sh
sudo install -m 0755 wdx-hub /usr/local/bin/wdx-hub
sudo install -m 0644 deploy/hub/wdx-hub.service /etc/systemd/system/wdx-hub.service
sudo systemctl daemon-reload
hub setting set --name public_url --value https://hub.example.com
sudo systemctl enable --now wdx-hub
journalctl -u wdx-hub
hub version
```
