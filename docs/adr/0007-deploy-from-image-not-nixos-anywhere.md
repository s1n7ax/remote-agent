# Deploy from a prebuilt DigitalOcean image, then idle-safe auto-deploy

The droplet is created from a NixOS image built on the user's machine (`nix build .#do-image`), not with nixos-anywhere, which needs more RAM than a cheap droplet has. Afterwards a timer pulls `main`, drains the app until no run is active, switches, health-checks and rolls back on failure, so a deploy never kills an agent mid-run.
