# Reachable only over Tailscale

The app and the preview proxy bind 127.0.0.1 only; `tailscale serve` publishes them to the tailnet over HTTPS (needed for Web Push), and the firewall opens nothing publicly but Tailscale's UDP port. There is no in-app login: tailnet membership is the authentication, and state-changing API requests require an `X-Requested-With` header to block cross-site requests.
