# Ticket images stay on the board

Images the user attaches to a ticket are stored only on the board server and handed to runs through the ticket's checkout. They are never uploaded to GitHub: the issue only says how many images the ticket has, and the app never commits them. Screenshots and phone photos can show private things, and anything put on GitHub is as visible as the repo, while the board is reachable only on the tailnet (ADR 0006). The cost is that reviewers on GitHub do not see the images, and a link to them from the issue would work only for the user.

## Considered Options

- Show the images on the issue (uploaded as GitHub attachments or committed to a branch). Rejected: it publishes them, and taking that back later is not possible once they have been seen or cached.
