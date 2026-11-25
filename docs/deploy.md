# Deploying remote-agent on a DigitalOcean droplet

The flake ships an example host, `nixosConfigurations.droplet`
(`nix/hosts/droplet.nix`), and a DigitalOcean custom image of it,
`packages.x86_64-linux.do-image`. The image is built on your machine, so the
2 GB droplet never runs an installer (no nixos-anywhere).

The app binds `127.0.0.1` only. `tailscale serve` publishes it on the tailnet
over HTTPS (port 443, previews on 8443). The firewall opens only Tailscale's
UDP port, plus SSH (key-only) until you close it.

## 1. Adjust the host (optional)

Fork the repo or copy `nix/hosts/droplet.nix` into your own flake and set:

- `droplet.sshKeys` — extra root SSH keys (keys picked when creating the
  droplet are fetched from DigitalOcean's metadata anyway).
- `services.remote-agent.autoDeploy.flake` — the flake auto-deploy follows,
  e.g. `github:<you>/remote-agent#droplet`.
- `services.remote-agent.publicURL` / `previewURL` once you know the
  tailnet name (`https://remote-agent.<tailnet>.ts.net`, `:8443`).

## 2. Build the image

```sh
nix build .#do-image
ls result/   # nixos-image-digital-ocean-*.qcow2.gz
```

Building needs x86_64-linux (or a remote builder) and a few GB of disk.

## 3. Upload it as a custom image

Web UI: **Backups & Snapshots → Custom Images → Upload Image**, pick the
`.qcow2.gz`, distribution "Unknown", choose a region.

Or with `doctl` (the file must be reachable by URL, e.g. a public object in
Spaces):

```sh
doctl compute image create remote-agent-nixos \
  --region fra1 \
  --image-url https://<bucket>.fra1.digitaloceanspaces.com/nixos-image-digital-ocean.qcow2.gz \
  --image-distribution Unknown
doctl compute image list-user   # wait for status "available"
```

## 4. Create the droplet

A Basic 2 GB / 1 vCPU droplet from the custom image, in the image's region,
with your SSH key selected:

```sh
doctl compute droplet create remote-agent \
  --region fra1 --size s-1vcpu-2gb \
  --image <custom-image-id> --ssh-keys <key-fingerprint>
```

The host turns on zram swap (`memoryPercent = 100`, zstd) so agents and
`nixos-rebuild` builds fit into 2 GB.

## 5. Join the tailnet

```sh
ssh root@<droplet-public-ip>
tailscale up            # prints a login URL
```

For unattended joins, create an auth key in the Tailscale admin console, put
it in a root-only file and set
`services.remote-agent.tailscale.authKeyFile = "/var/lib/secrets/tailscale-authkey";`.

Enable **HTTPS certificates** in the tailnet's DNS settings (needed for
`tailscale serve --https` and for Web Push). The
`remote-agent-tailscale-serve` unit waits until Tailscale is up and then runs

```sh
tailscale serve --bg --https=443  http://127.0.0.1:7700
tailscale serve --bg --https=8443 http://127.0.0.1:7702
```

Check with `tailscale serve status`. After this, SSH over the tailnet
(`ssh root@remote-agent`) and set `services.openssh.openFirewall = false` to
close port 22 on the public interface.

## 6. Secrets

Secrets are files outside the Nix store, read by systemd as credentials
(`LoadCredential`), so they stay root-only:

```sh
install -d -m 0700 /var/lib/secrets/remote-agent
install -m 0400 /dev/stdin /var/lib/secrets/remote-agent/github-token   # paste the fine-grained PAT, then Ctrl-D
install -m 0400 /dev/stdin /var/lib/secrets/remote-agent/openrouter-key # OpenRouter key (Pi runs)
systemctl restart remote-agent
systemctl status remote-agent
```

The service does not start until `githubTokenFile` exists. Set
`openrouterKeyFile = null` if you do not use Pi. Claude accounts are added in
the app (Accounts), not here.

## 7. Sign in

The app creates a login token on first start. Print a one-tap pairing link:

```sh
ssh root@<droplet> 'echo "https://remote-agent.<tailnet>.ts.net/#/login?token=$(cat /var/lib/remote-agent/login-token)"'
```

Open it on your phone (or paste the token on the sign-in screen); the browser
stays signed in. Add the app to the home screen (needed for push
notifications on iOS). To sign every browser out, delete the `session_secret`
setting or replace the token file and restart the service.

## How updates flow

`services.remote-agent.autoDeploy` adds the `remote-agent-auto-deploy` timer
(every `interval`, default 10 min). Each run:

1. resolves the flake's latest revision (`nix flake metadata --json --refresh`)
   and compares it with the running system's `configurationRevision`
   (`nixos-version --configuration-revision`; the flake sets it from
   `self.rev`). Equal → nothing to do. A revision that failed before is skipped.
2. `POST /api/drain {"drain":true}` so no new agent runs start, then polls
   `GET /api/idle` every 30 s until it is `{"idle":true}`. After
   `maxWaitHours` (default 6) it undrains and tries again next time.
3. `nixos-rebuild switch --flake <locked url>#droplet` — the exact revision
   resolved in step 1.
4. `remote-agent healthcheck` up to `healthRetries` times (6 s apart). On
   failure: `nixos-rebuild switch --rollback`, the revision is remembered as
   failed, and the app is checked again.
5. `POST /api/drain {"drain":false}`.

So: merge to `main` → the droplet deploys within one interval after the
running agents finish, and rolls back by itself if the new version is
unhealthy.

```sh
journalctl -u remote-agent-auto-deploy     # what it did and why
systemctl start remote-agent-auto-deploy   # deploy now
rm /var/lib/remote-agent-deploy/failed-rev # retry a revision that failed
```

## Updating hashes after dependency changes

`nix/package.nix` holds `vendorHash` (Go modules) and `npmDepsHash`
(`web/package-lock.json`). After changing dependencies, set the hash to
`lib.fakeHash`, run `nix build .#remote-agent 2>&1 | grep got:` and paste the
`got:` value back.
