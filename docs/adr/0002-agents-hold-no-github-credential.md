# Agents hold no GitHub credential; the app has no merge tool

Every GitHub action an agent needs (push a non-default branch, open/update a PR, read CI and logs, comment) goes through the app's MCP server, which alone holds the token. No MCP tool merges; only the user's merge button calls the merge API, pinned to the head SHA the user saw. Rejected: giving agents a token plus branch rulesets, because any token with contents:write can still merge or push to the default branch.
