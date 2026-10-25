# Security

## Reporting a problem

Please don't open a public issue for a security problem. Report it privately through [GitHub's security advisories](https://github.com/sjdsjadjajdjsa/prism/security/advisories/new) instead. You'll get an answer within a few days, and credit in the fix if you'd like it.

Useful to include: what you found, how to reproduce it, and the commit you're on (`git -C ~/.prism log -1 --oneline`).

## What counts

- Prism's commands or files reaching outside the folder it was started in
- The router answering requests from other machines or from web pages
- Keys, sign-ins or the router secret leaking
- Commands that should ask first running without asking

Free AI services keeping what you send them is expected and documented, not a vulnerability.

## Supported versions

Only the latest release gets fixes. `prism update` gets it.
