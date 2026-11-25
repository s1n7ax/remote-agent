# Drive the unmodified Claude CLI, never the API

Claude Pro/Max subscriptions may only be used through Anthropic's official CLI for personal use, so every Claude run spawns the unmodified `claude` binary in stream-json mode with a `claude setup-token` token as `CLAUDE_CODE_OAUTH_TOKEN`. The app never calls the Anthropic API with that token, never passes `--bare`, and strips `ANTHROPIC_API_KEY` from run environments. Rejected: the Agent SDK or direct API calls, which would be simpler to drive but break the subscription terms.
