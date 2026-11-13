# Security policy

## Reporting a vulnerability

Please report security problems privately: on this repository's **Security** tab, choose **Report a vulnerability**. Don't open a public issue for them.

Say what you found and how to reproduce it, with the planka-mcp version (`planka-mcp version`), the transport (stdio or http) and the Planka version.

planka-mcp is maintained by one person, who aims to reply within a week. Once a fix is released, the advisory is published, with credit to you unless you'd rather not.

## Supported versions

Fixes go into the latest release only.

## What is in scope

planka-mcp holds Planka API keys and passwords, and in http mode serves several people at once. Reports about these matter most:

- a key, password or session token that leaks, into logs, error messages or another user's results;
- one user acting with another user's key;
- a write that reaches a card, board or comment other than the one asked for.

A problem in Planka itself belongs to the [Planka project](https://github.com/plankanban/planka/security).
