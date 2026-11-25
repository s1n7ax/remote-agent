# Go single binary with SQLite and an embedded Svelte SPA

Idle memory must stay well under the 2 GB droplet's budget while agents run, and deployment should be one Nix package. A Go binary with a pure-Go SQLite driver and the SPA embedded via `go:embed` idles at tens of MB and needs no Node at runtime. Rejected: a Node/TypeScript backend (higher idle RSS) and Rust (slower to build this much).
