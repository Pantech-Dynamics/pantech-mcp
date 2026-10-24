# Changelog

Releases are cut from this file: a new `## vX.Y.Z — YYYY-MM-DD` heading pushed to main is
tagged and released (binaries on GitHub, the image on ghcr.io).

## v0.1.0 — 2026-10-24

First release.

- `pantech-mcp stdio` for a local AI app, with the Pantech CLI's sign-in or `PANTECH_API_KEY`.
- `pantech-mcp http`, the hosted Streamable HTTP server at `https://mcp.pantechdynamics.com/mcp`:
  OAuth through the console (RFC 9728 protected resource metadata), each caller's own key.
- 54 tools across instances, SSH keys, VPC networks, subnets and firewalls, public IPs and port
  forwarding, security groups, load balancers, volumes, snapshots, managed databases, Kubernetes,
  billing, usage, webhooks and operations.
- Two-step confirmation (preview with the price, then a signed, 5-minute, argument-bound token)
  for everything that costs money, opens access from the internet, or deletes or overwrites.
- The agent brief resource and the `deploy_web_app`, `explain_my_bill` and `lock_down_firewall`
  prompts.
- The Pantech logo in the server's identity, served at `/icon.svg`.
- `pantech-mcp http` checks each key with the API (`GET /me`) before serving `/mcp`, remembering
  only the answer by the key's hash (30 s for a good key, never past its expiry; 10 s for a bad
  one). An expired, revoked or unknown key gets `401` with
  `WWW-Authenticate: Bearer error="invalid_token", resource_metadata="…"`, so AI apps sign in
  again; when the API cannot answer, `503` with `Retry-After`, never `401`.
- Behind a trusted proxy (`MCP_TRUST_PROXY=true`), every API call carries the caller's address in
  `X-Pantech-Client-IP`, so the API's per-address limits and key audit see the AI app's address,
  not the MCP server's.
- Rate limits sized for hosted AI apps that share a few addresses: `MCP_RATE_LIMIT_IP` 1200 a
  minute, `MCP_RATE_LIMIT_TOKEN` 120, and a new `MCP_RATE_LIMIT_AUTH_FAILURES` (60 a minute per
  address) for requests without a good key.
- `pantech-mcp stdio` refuses a CLI `config.json` or `credentials.json` other users can write, and
  an API URL that is not https (or localhost); `pantech-mcp http` refuses an empty
  `MCP_AUTHORIZATION_SERVER` instead of falling back to the production console.
