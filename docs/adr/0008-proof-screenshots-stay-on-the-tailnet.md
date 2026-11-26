# Proof screenshots stay on the tailnet; the PR proof is text-only

GitHub can only show an image it can fetch, and the app is reachable only over the tailnet (ADR 0006), so proof screenshots cannot appear inline in the PR comment unless they are copied to GitHub. We keep them off GitHub: the PR proof comment carries the report as text with a link to the ticket's Proof tab, and the screenshots render only in the app. Screenshots of the user's app are therefore never as visible as the repository itself.

- Rejected: committing screenshots to a screenshots-only branch of the repo. Images would show inline in the PR, but anyone who can see the repository could see them, and every proof would add files to the repository's history.
