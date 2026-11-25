# One bubblewrap sandbox per run

Each run sees, read-write, only its ticket's own clone (`work/<ticket>/`, also the workdir) and the project's agent `$HOME`; read-only, `/nix/store`, system paths and the skills snapshot. It never sees the app's clone (`repo/`), other tickets' clones, other projects, or the app's database and tokens. Agents run with permissions bypassed inside it.

Because the app runs as the same uid outside the sandbox, whatever a sandbox can write is hostile to it: the app never runs git that reads an agent clone's config or hooks outside a sandbox (it pushes from its own `repo/` after fetching the branch through a sandboxed, read-only `upload-pack`), and it touches files in those directories only through `os.Root`. Ticket clones are independent (no shared object store, hardlinks or alternates), so one ticket cannot poison another or the app's clone. Skill benchmarks run in the same sandbox.

Rejected: per-project system users (projects are added at runtime), containers (too heavy for a 2 GB droplet), and shared git worktrees of `repo/` (a worktree can write the main repository's config and hooks, which the app then runs).
