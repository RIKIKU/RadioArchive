# Deploying RadioArchive

RadioArchive is two pieces on two kinds of host:

- **The server**: the API (with the listener and admin pages built into it), its PostgreSQL, Authentik for sign-in, and the nightly backup, in Docker Compose on a Linux PC (amd64), behind your own reverse proxy, which does TLS.
- **The recorder host**: trunk-recorder with RadioArchive's recorder plugin, beside the recorder client, which encodes calls to Opus and sends them to the server. amd64 or arm64; a Raspberry Pi 4 is enough. Each recorder is a host of its own.

This guide assumes you know Docker, Docker Compose and your reverse proxy. Commands run in the folder with the server's `compose.yaml` unless said otherwise.

- [Before you start](#before-you-start)
- [Install the server](#install-the-server)
- [The reverse proxy](#the-reverse-proxy)
- [The first admin and the setup](#the-first-admin-and-the-setup)
- [The recorder host](#the-recorder-host)
- [Your own changes](#your-own-changes)
- [Sizing the recordings volume](#sizing-the-recordings-volume)
- [Backups](#backups)
- [Upgrading](#upgrading)
- [Automatic upgrades](#automatic-upgrades)
- [Monitoring](#monitoring)
- [What's left to you](#whats-left-to-you)

## Before you start

You need:

- a Linux server with Docker and the Compose plugin, about 1 GB of memory for RadioArchive and Authentik, and disk for the recordings ([Sizing](#sizing-the-recordings-volume));
- two subdomains under one domain, pointing at your reverse proxy: one for the radio (`radio.example.org`) and one for its Authentik (`auth.example.org`);
- a mail server Authentik can send through, for listeners' sign-up codes;
- trunk-recorder's `config.json` for your system, on each recorder host.

## Install the server

1. Download a release's files into a folder of their own, and unpack the configuration archive there:

   ```sh
   mkdir radioarchive && cd radioarchive
   base=https://github.com/RIKIKU/RadioArchive/releases/latest/download
   curl -fLO $base/compose.yaml
   curl -fLO $base/env.example
   curl -fLO $base/radioarchive-config.tar.gz
   tar -xzf radioarchive-config.tar.gz
   ```

2. **Check the files** (optional, recommended). Each file and image of a release is attested by the release's run on GitHub. With the [GitHub CLI](https://cli.github.com/):

   ```sh
   gh attestation verify compose.yaml --repo RIKIKU/RadioArchive
   gh attestation verify radioarchive-config.tar.gz --repo RIKIKU/RadioArchive
   gh attestation verify oci://ghcr.io/rikiku/radioarchive:0.1 --repo RIKIKU/RadioArchive
   ```

3. Save `env.example` as `.env` and fill it in. Each secret is any long random string: `openssl rand -base64 36 | tr -d '/+='` makes one. Keep it readable by you only: `chmod 600 .env`. The settings are explained in the file. At least:
   - `APP_URL` and `AUTHENTIK_URL`, the two public addresses;
   - `INITIAL_ADMIN_EMAIL`, your own address: the first account to sign in with it becomes the first admin;
   - the secrets, Authentik's administrator (`akadmin`, for you as the operator, not a radio account), and `SMTP_*`;
   - `TZ`, your time zone;
   - `TRUSTED_PROXY`, once you know your proxy's address ([The reverse proxy](#the-reverse-proxy)).

4. Start it:

   ```sh
   docker compose up -d
   docker compose ps     # api, db, backup, authentik-server, authentik-worker
   curl -s http://127.0.0.1:8080/healthz    # ok
   ```

   The API applies its database migrations, then answers. Authentik takes a minute or two on its first start; listening never needs it.

Both published ports bind to `127.0.0.1` (`API_BIND`, `AUTHENTIK_BIND`): only your reverse proxy should reach them.

### With an Authentik you already run

Until the combined deployment with PagerArchive (`SPEC.md` R105) ships, RadioArchive can still use an Authentik you already run, in place of its own:

1. In `compose.override.yaml`:
   - give `authentik-server` and `authentik-worker` `profiles: [off]`, so they don't start;
   - set the API's `AUTHENTIK_INTERNAL_URL` to an address where it reaches your Authentik (a shared Docker network, or leave it unset to use `AUTHENTIK_URL`);
   - set `AUTHENTIK_API_TOKEN: ""` if you don't create the accounts service account (step 3).
2. `compose.yaml` still needs every `:?` setting in `.env`, so give the Authentik-only ones (`AUTHENTIK_SECRET_KEY`, `AUTHENTIK_ADMIN_*`, `SMTP_HOST`, `SMTP_FROM`) placeholder values. `db` still creates an empty `authentik` database, which the backup dumps and checks with the rest; your Authentik's own backup covers its data.
3. Apply `deploy/authentik/blueprints/radioarchive.yaml` in your Authentik, with `RA_APP_URL` and `RA_OIDC_CLIENT_SECRET` set (your `APP_URL` and `OIDC_CLIENT_SECRET`), for example through `ak shell`'s blueprint importer. Its last two sections, the accounts service account and the removed entries, are optional: without the service account, disabling an account changes the radio only, and the admin pages say so. Leave out `radioarchive-standalone.yaml`: it's the standalone sign-up and brand.
4. The first admin's Authentik user needs the `email_proven` attribute set to their address before they sign in ([ADR 0025](adr/0025-admin-accounts-roles-and-admin-sign-in.md)). Otherwise `INITIAL_ADMIN_EMAIL` never matches a proven address.

## The reverse proxy

Your proxy terminates TLS and forwards each subdomain to its port. The release's archive has a working nginx example, `deploy/nginx/radioarchive.conf`. It:

- **turns buffering off and sets long read timeouts** for the listener's live WebSocket (`/api/v1/live`, open for hours) and for zips, which are built while they stream;
- **overwrites `X-Forwarded-For`** with the address that connected to nginx: `proxy_set_header X-Forwarded-For $remote_addr;`;
- **denies `/metrics`**, which only the monitoring stack reads, from inside;
- has an optional `limit_req`, commented out, in front of the API's own limits.

### TRUSTED_PROXY

RadioArchive limits heavy use per client (an IPv4 address, or an IPv6 /64). Behind a proxy every request comes from the proxy, so the API reads the client's address from `X-Forwarded-For`, but **only** for requests from the one address in `TRUSTED_PROXY`, and only the **last** address in it, the one your proxy wrote. Set `TRUSTED_PROXY` to your proxy's address as the API sees it, one exact address, never a range:

- nginx on the same host, the API's port on `127.0.0.1`: the Docker network's gateway, which `docker compose exec api` can't print (the image has no shell), so look it up: `docker network inspect radioarchive_default -f '{{(index .IPAM.Config 0).Gateway}}'`;
- a proxy container on a Docker network shared with the API: that container's address on it;
- a proxy on another host: its address.

Then `docker compose up -d`. Unset, every listener counts as one client and they share one set of limits.

Because the proxy overwrites the header, a listener can't send an address of their own and be counted as someone else.

### Behind another proxy (a chain)

If traffic passes through another proxy first (an edge host, a tunnel) before reaching the nginx in front of RadioArchive, the inner nginx (or SWAG) must restore the visitor's address before overwriting `X-Forwarded-For`. In its `http` or `server` block, trust only the forwarder in front of it, by its exact address:

```nginx
set_real_ip_from 192.0.2.10;      # the forwarder in front of this proxy
real_ip_header X-Forwarded-For;
real_ip_recursive on;
```

`$remote_addr` is then the visitor's address, and the example's `proxy_set_header X-Forwarded-For $remote_addr;` sends that to RadioArchive. `TRUSTED_PROXY` stays the inner proxy's address. If the inner proxy appended rather than overwrote, every visitor would count as the forwarder, one client, and hit the request limit almost at once.

**SWAG**: copy the example's two server blocks into `nginx/proxy-confs/radioarchive.subdomain.conf` (its `include` lines for SSL replace the commented `ssl_certificate` lines), and put the three `real_ip` lines in `nginx/site-confs/default.conf`'s `server` block, or in `nginx/nginx.conf`'s `http` block, when SWAG sits behind another proxy.
### Behind a sign-in gate

To keep the radio private for a while, for example behind Authentik's forward auth in your proxy, gate everything except:

- `/api/v1/recorder/`, which recorder clients reach with their client key and no browser;
- `/healthz`, if anything checks it from outside.

Everything else, the live socket and zips included, can sit behind the gate. A gate that signs people in to the same Authentik as the radio means one sign-in: the radio's own **Sign in** then completes without a second password. A recorder host on your network can skip the proxy and reach the API directly, through a port that serves only `/api/v1/recorder/`.

### Checking it

From outside: `https://radio.example.org/` opens the radio; `https://radio.example.org/metrics` answers 404; **Start listening** plays live calls once a recorder is attached.

## The first admin and the setup

1. Open the radio, choose **Sign in** in the account menu and sign up with `INITIAL_ADMIN_EMAIL`: Authentik emails a code. While no admin exists, that account becomes one. Admins promote others on **Admin → Accounts**.
2. Create your system on **Admin → Systems** (or import a whole setup with the PowerShell module, `Import-RadioArchiveSetup`). The module is on the PowerShell Gallery, published with each release: `Install-PSResource -Name RadioArchive` in PowerShell 7.6 or later, and `Update-PSResource -Name RadioArchive` after an upgrade. Its version is the release's.
3. Add each recorder, from the server's shell only. It prints the recorder's client key once:

   ```sh
   docker compose exec api radioarchive recorder add "North hill"
   ```

   Keep the key for the recorder host's `.env`. `recorder rotate-key` and `recorder retire` replace or revoke it.
4. Once the recorder has connected, attach its trunk-recorder `shortName` to your system on **Admin → Systems**. Until then its calls are rejected and counted.

## The recorder host

### With Docker

On the recorder host, in a folder of its own:

```sh
base=https://github.com/RIKIKU/RadioArchive/releases/latest/download
curl -fLO $base/recorder-compose.yaml
curl -fL -o .env $base/recorder-env.example
chmod 600 .env
mv recorder-compose.yaml compose.yaml
```

Fill in `.env`: `SERVER_URL` (the radio's public address) and `CLIENT_KEY` (from `recorder add`). Put your trunk-recorder `config.json` beside it, with `"captureDir": "/app/media"`, `"audioStreaming": true`, and the plugin in its `plugins`:

```json
"plugins": [
  { "name": "radioarchive", "library": "libradioarchive.so", "port": 9850 }
]
```

Then `docker compose up -d`. The image is trunk-recorder's official image of the release it was built against, with the plugin added. The client shares trunk-recorder's network (the plugin sends only to 127.0.0.1) and its media folder, uploads each finished call, and deletes trunk-recorder's file once the server has it. While the server can't be reached it keeps recordings on disk, for up to `QUEUE_MAX_AGE` (24 hours).

### For a recorder you share with another uploader

If trunk-recorder already feeds another site through another uploader plugin, you can add RadioArchive without changing what that recorder does. Leave `audioArchive` as it is (usually `false`) and give the plugin a `handoffDir` ([ADR 0045](adr/0045-the-handoff-directory.md)):

```json
{ "name": "radioarchive", "library": "libradioarchive.so", "port": 9850, "handoffDir": "/app/media/radioarchive-handoff" }
```

At each call's end the plugin hard-links the finished WAV into `handoffDir` under a name of its own and tells the client that name. trunk-recorder deletes or keeps its own file as it always did, and the other uploader sees nothing new. The client deletes only its link, once the server has the recording.

- `handoffDir` is an absolute path, as trunk-recorder sees it, **on the same mount as the recordings**: a hard link can't cross mounts, even two bind mounts of one disk. Put it inside the folder or volume that holds trunk-recorder's recordings (its `captureDir`, `/app/media` above, or trunk-recorder's working folder when `captureDir` isn't set). Create it before starting trunk-recorder, writable by trunk-recorder's user.
- The client must see the links at the same path, and be able to delete them. It doesn't need the rest of trunk-recorder's folder: mount just the hand-off folder, read-write, at the same path in the client.
- If a link fails, trunk-recorder's log says why (at most once a minute) and the plugin names trunk-recorder's own file, as without `handoffDir`. With `audioArchive` off that recording is lost to RadioArchive; nothing else changes.
- A link is deleted only by the client. If the client is stopped for long, or misses a call's end, links stay. A daily `find <the hand-off folder> -type f -mmin +1500 -delete` (25 hours, just over `QUEUE_MAX_AGE`) clears them.

### Without Docker

Each release also has the plugin (`libradioarchive-amd64.so`, `libradioarchive-arm64.so`) and the client (`radioarchive-client-amd64`, `radioarchive-client-arm64`), with `SHA256SUMS`:

```sh
sha256sum -c --ignore-missing SHA256SUMS
gh attestation verify libradioarchive-arm64.so --repo RIKIKU/RadioArchive
```

- The plugin goes in trunk-recorder's plugin folder (`/usr/local/lib/trunk-recorder/libradioarchive.so`); it must be built for the trunk-recorder release the release notes name. For a recorder you share with another uploader, see [above](#for-a-recorder-you-share-with-another-uploader).
- The client needs `libopus0`. Run it under systemd as the user that owns trunk-recorder's media folder, with `SERVER_URL`, `CLIENT_KEY` (or `CLIENT_KEY_FILE`) and `QUEUE_DIR` set (`radioarchive-client` with no settings lists them).

### Upgrading a recorder

Recorder clients and the plugin are upgraded by hand. The server accepts clients up to one minor version behind its own and flags them on **Admin → Recorders**; an older one is refused, naming both versions. With Docker, get the new release's `recorder-compose.yaml`, then `docker compose pull && docker compose up -d`.

## Your own changes

Put every change of your own in `compose.override.yaml`, next to `compose.yaml`: an upgrade replaces `compose.yaml` and `deploy/`, never the override or `.env`. For example, the recordings on a disk of their own:

```yaml
services:
  api:
    volumes:
      - /mnt/recordings:/var/lib/radioarchive/recordings
```

(the folder must be writable by uid 65532).

## Sizing the recordings volume

Recordings are Opus at about 12 kbit/s. A month's need is about

> **disk ≈ call-hours × 5.7 MB + calls × 2.1 KB**

and the volume should be that month's need × (retention days + 1) / 30 × 1.5. For example, 300 call-hours and 60,000 calls a month is about 1.8 GB a month; with 30 days' retention, give it 1.8 × 31 / 30 × 1.5 ≈ 2.8 GB.

Below `RECORDINGS_MIN_FREE` (5 %) free, the oldest recordings are deleted early, logged, counted and alerted as "retention shortened", but never one from the last day (`RECORDINGS_EARLY_DELETE_FLOOR`). Admins are warned at 15 % free.

## Backups

The `backup` service backs up both databases every night at `BACKUP_AT` (04:00), into `BACKUP_DIR` (`./backups`), keeping `BACKUP_KEEP_DAYS` (14) nights. Each night's dumps are restored into a throwaway database and checked, table by table. The admin pages show an alert when the last backup failed or the last good one is over 36 hours old.

### What's in a backup

- RadioArchive's database: systems, talkgroups, units, groups, tags, attachments, recorders' key hashes (not the keys), settings, the audit log, accounts and signed-in listeners' saved settings. **Not** call data: calls, transmissions and rejected calls are left out.
- Authentik's database: listeners' and admins' sign-in.
- `NIGHT.setup.json`: the setup export, the file the PowerShell module imports.
- **Not the recordings.** They're kept for the retention period only, and aren't backed up.

**Keep these safe yourself**: they aren't in any backup.

- `.env` (the secrets: without `AUTHENTIK_SECRET_KEY` and the passwords, a restore can't sign anyone in);
- `compose.override.yaml`, if you made one;
- your trunk-recorder `config.json` and each recorder's `.env` (its client key).

Copy the backup folder off the host with your own tool; the files are readable by `BACKUP_UID`:`BACKUP_GID` only.

Back up at once: `docker compose exec backup radioarchive-backup now`. See the last runs: `docker compose exec backup radioarchive-backup status`.

### Restoring

Into an empty install (a new host, or after `docker compose down -v`, which deletes the databases **and the recordings volume**), with your `.env` and the backup folder in place:

```sh
docker compose up -d db
docker compose run --rm backup restore 2026-11-03
docker compose up -d
```

It refuses an install that isn't empty, and a night whose check failed unless you add `--unchecked`. Calls and recordings start again empty; recorders reconnect with their existing keys.

## Upgrading

Releases come out weekly when anything but the documentation changed, and straight away for a security fix. **A patch release** (0.1.0 to 0.1.1) needs only:

```sh
docker compose pull && docker compose up -d
```

Your `compose.yaml` names the minor version you have (`radioarchive:0.1`), so pulling keeps you on its patches. **A new minor or major version** comes with new files: download that release's `compose.yaml` and archive first, then pull and up.

```sh
base=https://github.com/RIKIKU/RadioArchive/releases/latest/download
curl -fLO $base/compose.yaml
curl -fL $base/radioarchive-config.tar.gz | tar -xz
docker compose pull && docker compose up -d
```

`.env` and `compose.override.yaml` stay as they are, and database migrations run when the API starts. Listeners' live connections drop while the API restarts and reconnect by themselves, catching up on what they missed. `docker compose exec api radioarchive version` says what's running. Before version 1.0.0 a release can break the upgrade; if so its release notes start with "Upgrade steps".

## Automatic upgrades

The server can upgrade itself every hour, with a backup first and a rollback if the upgrade fails. It's off until you switch it on:

```sh
deploy/auto-upgrade/auto-upgrade.sh enable     # hourly from your crontab
docker compose up -d                           # so the API reads its status
```

(`enable --systemd` uses a systemd user timer instead.) Each run takes the newest release in the same major version and:

1. **verifies** its files and image with `gh attestation verify` (a pinned GitHub CLI it fetches and checks);
2. **backs up**: the backup service's backup, kept aside in `backups/pre-upgrade/`, and copies of `compose.yaml`, `.env`, `compose.override.yaml` and `deploy/`;
3. **upgrades**: the new files, `pull`, `up -d`;
4. **checks**: the API answers as the new version, every service runs, your own `AUTO_UPGRADE_CHECK_URLS` answer, and, if at least 20 calls arrived in the 2 hours before, a call arrives within 30 minutes;
5. **rolls back** if any of that fails: the previous files and image, and the database from the backup if the new version changed it. That release is then marked failed and isn't tried again.

It never runs while a backup runs, or while another app on the host upgrades: it holds `/run/lock/archive-upgrade.lock`, which PagerArchive's upgrade takes too. A failed upgrade shows on the admin pages and as the "RadioArchive upgrade failed" alert. `deploy/auto-upgrade/auto-upgrade.sh status` says what it last did; `retry X.Y.Z` tries a failed release again; `clear` clears a run that stopped part way, once you've checked the install; `disable` switches it off. It never upgrades recorder hosts.

## Monitoring

RadioArchive exports metrics and ships alert rules and dashboards for **PagerArchive's example monitoring stack** (Grafana Alloy, Prometheus, Grafana, and more). Run that stack beside PagerArchive, or on its own from [PagerArchive's release](https://github.com/RIKIKU/PagerArchive/releases/latest) (v1.23.0 or later), as its `docs/monitoring.md` says.

1. Set `METRICS_TOKEN` in RadioArchive's `.env` (at least 16 characters).
2. Join RadioArchive's API to the stack's `monitoring` network, in `compose.override.yaml`:

   ```yaml
   services:
     api:
       networks:
         default:
         monitoring:
           aliases: [radioarchive]
   networks:
     monitoring:
       external: true
   ```

   then `docker compose up -d`. Keep `default`, or the API loses its database.
3. Copy RadioArchive's files into the stack's include folders (in the stack's folder):

   ```sh
   ra=/path/to/radioarchive/deploy/monitoring
   cp $ra/alloy.d/radioarchive.alloy include/alloy.d/
   printf '%s' "$METRICS_TOKEN" > include/alloy.d/radioarchive-metrics-token
   chmod 600 include/alloy.d/radioarchive-metrics-token
   cp $ra/alerts.d/radioarchive.yaml include/alerts.d/
   cp -R $ra/dashboards.d/RadioArchive include/dashboards.d/
   docker compose restart alloy grafana
   ```

Grafana then has a **RadioArchive** folder with three dashboards and the alert rules, which go to the stack's default notification policy. The rules: recorder disconnected or deaf for 2 minutes, quiet (where set), calls from an unattached recorder system, recordings missing, live delay over 2 s, the recordings disk and retention shortened or the volume full, a client's queue over an hour old, the API unreachable, backups failing, an upgrade failed, and the radio full for 10 minutes. Thresholds are each rule's `params:`. Copy the files again after an upgrade that changes them.

Nothing exported names a talkgroup or a unit, or holds a listener's address.

## What's left to you

RadioArchive limits heavy use itself, but leaves these to your deployment: TLS, connection floods and DDoS, blocking by country, fail2ban and firewalling. The nginx example's `limit_req` is a start.
