# One bubblewrap sandbox per run

Each run sees only its project directory (as `$HOME` and workdir), a read-only `/nix/store`, the read-only skills snapshot and its own config dirs, never other projects or the app's database and tokens. Agents run with permissions bypassed inside it. Rejected: per-project system users (projects are added at runtime) and containers (too heavy for a 2 GB droplet).
