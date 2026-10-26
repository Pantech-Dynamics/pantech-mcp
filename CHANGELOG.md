# Changelog

Releases are cut from this file: a new `## vX.Y.Z — YYYY-MM-DD` heading pushed to main is
tagged and released (binaries on GitHub, the image on ghcr.io).

## Unreleased

### Added

- Inside an instance: `start_tools_session` opens a session on an instance whose Pantech agent is
  enabled (`get_instance_agent`), in `plan` (reads only) or `ask` mode, and `run_in_instance`,
  `read_instance_file`, `write_instance_file` and `list_instance_dir` carry out one step each, as
  the session's unprivileged user, waiting up to a minute for the result (`get_agent_tool_call`
  follows a longer one). A step waiting for the user's approval is handed back at once with where
  to approve it: an AI app cannot approve its own steps. What the instance reports is returned as
  untrusted data only. Also `list_agent_sessions`, `get_agent_session`,
  `list_agent_session_events`, `end_agent_session` and `get_agent_session_report`.
- `create_instance` takes `agent: true` to install the Pantech agent at first boot, so an AI app
  can create an instance and then work in it with no login to it.

### Fixed

- A step that did not run because its approval was denied, or because nobody answered it in time,
  no longer says the user denied it.
- Every result's structured content now carries the summary and the untrusted-data notice beside
  the data, so a client that shows a model only structured content still sees what happened and
  what to do next.
- A step run in an instance returns its output once: the short preview and the fields that repeat
  the call's own id, tool and status are left out of the result.
- The `delete_instance` preview warns about a public IP only when one is mapped to the instance or
  forwards a port to it, and names it; an instance's own address no longer raises the warning.
- An instance tool refused to a standard API key (the local server's) says to use the hosted server,
  connected as an AI app: Pantech offers instance tools only there for now.

## v0.2.0 — 2026-10-24

### Added

- Instances: `rebuild_instance` ★ and `restore_instance_snapshot` ★ (destructive: the preview says
  what data is lost and that the AI app needs "Manage and delete" access), `get_instance_metrics`,
  `get_instance_usage`, `list_instance_volumes`, `list_instance_snapshots`,
  `create_instance_snapshot` ★ (priced from the snapshot quote) and `get_instance_order` (with
  `wait` to follow the order and its create operation).
- Temporary SSH access: `open_ssh_access` ★ opens port 22 for 15 minutes to the address Pantech
  sees the request come from. The preview names that address as this server sees it (the caller's
  address behind a trusted proxy; "this computer's public address" in stdio mode) and warns that
  through a web-based AI app it is the app's server, not the user. Once confirmed it waits for the
  grant and returns the host, port, expiry and a ready-to-run `ssh` command; the user's own SSH key
  is never seen. `get_ssh_access`, and `revoke_ssh_access` (one step: it only closes the port).
- Networking: `delete_network` ★, `delete_subnet` ★, `delete_public_ip` ★,
  `list_port_forwarding_rules`, `delete_port_forwarding_rule` ★, `update_load_balancer` ★
  (replacing targets is confirmed), `delete_load_balancer` ★, `create_security_group`,
  `delete_security_group` ★.
- Storage: `delete_snapshot` ★, `restore_snapshot` ★ (a new, billed volume, priced from the disk
  offering quote), `get_snapshot_schedule`, `set_snapshot_schedule` ★ (the preview says when
  lowering retention deletes old snapshots), `get_volume_usage`.
- Databases: `start_database`, `stop_database`, `resize_database_storage` ★ (priced from the
  engine's storage price), `delete_database` ★, `list_database_snapshots`,
  `create_database_snapshot` ★, `get_database_order`.
- Kubernetes: `delete_kubernetes_cluster` ★, `start_kubernetes_cluster`, `stop_kubernetes_cluster`,
  `list_node_pools`, `create_node_pool` ★, `delete_node_pool` ★, `list_kubernetes_addons`,
  `toggle_kubernetes_addon` ★, `list_cluster_nodes`, `list_cluster_workloads`.
- SSH keys: `delete_ssh_key` ★.
- Billing and webhooks: `get_usage_by_resource`, `list_receipts`, `list_webhook_deliveries` (read
  only; endpoint responses are not returned).
- Every tool that calls a route the API marks destructive is checked to be two-step and to say it
  needs "Manage and delete" access. Kubernetes manifests (read or applied) are added to the routes no
  tool may call, since they can hold Kubernetes secrets.

### Changed

- `get_credit_balance` states the available credit as an exact figure and labels the next invoice
  as a forecast that grows with usage until the bill date, for example: "Available credit:
  NGN 12,345.67 (exact, as of now). Next invoice so far: NGN 1,234.00 on 2026-11-01 — a forecast
  that grows with usage until the bill date." Its description tells the app to report amounts
  exactly, never rounded.
- `estimate_price` says its total is an estimate; `get_usage` and the usage tools say their figures
  are exact measured quantities. Money is formatted with integer arithmetic in each currency's minor
  unit (kobo, cents), so no amount is rounded.
- Temporary SSH access is no longer on the excluded list: a grant holds no secret.

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
