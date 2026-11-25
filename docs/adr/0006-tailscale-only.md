# Reachable only over Tailscale, and still signed in

The app and the preview proxy bind 127.0.0.1 only. `tailscale serve` publishes them to the tailnet over HTTPS (needed for Web Push), and the firewall opens nothing publicly but Tailscale's UDP port.

Tailnet membership alone is not enough, because agents share the host network: they could call the API on 127.0.0.1, or through the node's own tailnet address, and merge a PR. So every `/api` call needs one of two credentials:

- the session cookie a browser gets by pairing once with the login token;
- that token as a bearer, for host scripts like auto-deploy.

The token lives in the data dir, which no sandbox can see.

Three guards keep agents from getting or abusing the session:

- The preview proxy strips cookies, so agent-run dev servers never see the session.
- State-changing requests also need an `X-Requested-With` header, so a preview page cannot ride the session cross-origin.
- Rejected: a unix-socket listener alone. The tailnet address would still reach it through `tailscale serve`.
