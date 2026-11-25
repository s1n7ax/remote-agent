# /ship is an app-driven state machine, one run per stage

The app, not a long-lived agent, owns the ticket's stage and status: each stage is a fresh run that ends with `stage_done`, questions are posted as one tree per run and answering advances the ticket, and the app itself waits for CI (spending no tokens) and spawns fix runs. This lets runs survive account switches, restarts and day-long answer delays. Rejected: one agent session per ticket waiting on answers, which holds quota and dies with the process.
