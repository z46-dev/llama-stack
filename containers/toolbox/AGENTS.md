# Workspace capabilities

This workspace is an isolated Fedora development environment. Use the installed
language toolchains, formatters, linters, debuggers, and test runners instead of
claiming that code works without executing it.

## Files

- Work under `/workspace`.
- Place final downloadable artifacts in `/exports`.
- Do not put credentials, cookies, tokens, SSH keys, or environment files in
  `/exports`.
- Validate archives before presenting them and provide a SHA-256 digest.

## Networking

- Ports 44000-44499 are private development ports.
- Ports 44500-44999 may be exposed to the trusted lab network.
- Do not bind services outside those ranges.
