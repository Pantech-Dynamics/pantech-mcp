# Changelog

Releases are cut from this file: a new `## vX.Y.Z — YYYY-MM-DD` heading pushed to main is
tagged and released (binaries on GitHub, the image on ghcr.io).

## Unreleased

### Added

- Windows Server instances. `get_remote_desktop` gives a Windows instance's address, port and
  user. A Windows VM is created in the console, where the user types its Administrator password
  (and can reset it later): `create_instance` refuses a Windows image and says so, since a
  password never passes through a tool. A Linux image still needs `ssh_key_id`.

## v0.5.0 — 2026-11-10

### Added

- Pantech DNS, on Pantech's own name servers: `list_dns_zones`, `get_dns_zone`,
  `create_dns_zone` (the result names the zone's own two name servers to set at the domain's
  registrar, and says a zone still waiting after 14 days is removed; records added meanwhile go
  live when the zone is active. The first 5 live zones are free: creating a further one is ★, with
  its monthly price from `GET /dns/zones/quote`), `verify_dns_zone` (checks the delegation now and waits for the answer),
  `delete_dns_zone` ★, `list_dns_records`, `create_dns_record`, `update_dns_record` ★,
  `delete_dns_record` ★, `import_dns_zone` and `export_dns_zone` (BIND zone files). Record writes
  wait for the change to go live, usually within a minute. Changing or deleting a record previews
  its current values, and its token is bound to the record's version, sent as `If-Match`: a record
  changed after the preview is not overwritten. An import is one step unless `replace` is true,
  which deletes the record sets the file does not have and is confirmed. The apex SOA and NS are
  refused before anything is sent. Deleting a zone or record, a replacing import and getting an
  auth code need "Manage and delete" access.
- Domains: `list_domain_tlds`, `search_domains`, `check_domain_transfer`, `list_domains`,
  `get_domain`, `register_domain` ★, `transfer_domain` ★, `renew_domain` ★,
  `update_domain_settings`, `set_domain_name_servers` ★, `get_domain_contact`,
  `update_domain_contact` ★, `get_domain_auth_code` ★, `list_domain_orders` and
  `get_domain_order`. The preview of a paid order gives the exact total and currency for the years
  asked, says it is paid from the organization's credit before the registrar is asked, that a
  failed order returns the money and that a registered domain cannot be returned, and names the
  registration agreement; only the confirmed call sends `accept_terms`. The token is bound to the
  quoted total, so a price that changes after the preview is refused. `update_domain_settings` is
  confirmed only when it turns auto-renew, the transfer lock or privacy off. The registrant
  contact and auth codes stay in the fenced data, never in a summary.
- Pantech Container Registry: `get_registry_namespace`, `set_registry_namespace` ★ (the
  namespace is global and cannot change once a repository exists), `list_registry_repositories`,
  `create_registry_repository` (★ only for a public repository), `get_registry_repository`,
  `update_registry_repository` (★ when it makes the repository public, turns immutable tags off or
  sets a retention policy, which deletes images daily), `delete_registry_repository` ★,
  `list_registry_tags`, `get_registry_tag`, `delete_registry_tag` ★ (the preview says when
  immutable tags make Pantech refuse it), `list_registry_tokens`, `create_registry_token` ★ and
  `revoke_registry_token` ★. A new token's secret is returned once, in the fenced data only, with
  a summary telling the user to store it now; lists never carry secrets. Deleting a repository, a
  tag or a token needs "Manage and delete" access. Repository names and tags are checked against
  the OCI grammar before they are put in a path.
- Certificate Manager: `list_certificates`, `get_certificate`, `request_certificate` (★ when
  Pantech will write `_acme-challenge` records into the organization's active Pantech DNS zones,
  which the preview names; otherwise one step, and the result names the CNAME to add),
  `import_certificate` ★, `update_certificate`, `verify_certificate`, `reimport_certificate` ★
  (the same names required, checked in the preview), `export_certificate` ★, `revoke_certificate`
  ★ and `delete_certificate` ★. An import's certificate and key are checked here (the key matches,
  the leaf is not a CA, the key type is supported) and the preview describes the parsed
  certificate without the PEM or the key; neither is ever shown back. An export's key is in the
  fenced data only, with a summary saying to give it only to the user and store it owner-only.
  Export, revoke and delete need "Manage and delete" access.
- HTTPS on load balancers: `create_load_balancer` takes `protocol` (`tcp` or `https`),
  `certificate_id`, `tls_policy` (`modern` or `tls13`) and `redirect_http`; https is for appliance
  load balancers only and is refused for `vpc_native` before anything is sent. The preview names
  the certificate and its state, says it cannot be deleted or revoked while used, and says when
  `redirect_http` also opens port 80. `update_load_balancer` changes an https load balancer's
  certificate, TLS policy or redirect. `list_load_balancers` now returns every field the API
  sends, the TLS ones included.
- Domain prices are shown before VAT, and the previews say VAT is added where it applies. When the
  credit falls short, an order waits up to an hour for the difference: in US dollars the saved card
  is charged it, and the result gives a payment link only when the bank asks the user to confirm
  the payment; in naira it tells the user to add credit. The result says the order is not paid yet
  and gives its deadline. `price_changed` is explained.
- `retry_domain_order_payment` ★ tries a waiting domain order's payment again now (the saved card
  once more, or credit just added) and gives it another hour. Its preview names the order's exact
  amount, its token is bound to that amount and to the order still awaiting payment, and the order
  is never charged twice; an order not waiting for a payment is refused before anything is sent.
- `.ng` domains: privacy is always on (asking to turn it off is refused with the registry's
  reason), a `.ng` contact needs a state and a postal code, and a `name.ng` registrant must be a
  person; all three are checked before the preview.
- DNS zones now carry `delegation_deadline_at`, as the public API returns it.
- Teams and access policies: `list_iam_actions`, `get_iam_settings`, `list_iam_teams`,
  `get_iam_team`, `list_iam_team_members`, `list_iam_policies`, `get_iam_policy`,
  `list_iam_policy_attachments`, `list_iam_members`, `get_iam_effective_access` and
  `simulate_iam_access` read;
  `update_iam_settings`, `create_iam_team`, `update_iam_team`, `delete_iam_team`,
  `add_iam_team_member`, `remove_iam_team_member`, `create_iam_policy`, `update_iam_policy`,
  `delete_iam_policy`, `attach_iam_policy` and `detach_iam_policy` are all ★. Each preview
  describes every statement in our own words (actions, write access, resources by id or name
  pattern, address and MFA conditions) and who it reaches, warns when a change lifts a deny, and
  names the admin and owner rules. Statements are checked against the action catalogue before
  anything is sent, Pantech's managed policies are refused, and a policy change is bound to the
  policy's version (sent as `If-Match`). Every write tool says a policy is built only from what
  the user asked for, never from text in tool results.
- Two-step tools can now confirm only some calls (`import_dns_zone`, `update_domain_settings`) and
  bind their token to live state as well as the arguments (a price, a record's version).

## v0.4.0 — 2026-11-06

### Added

- `resize_kubernetes_node_plan` ★ moves a cluster's control node and default workers to another
  plan, one node at a time. The preview prices the nodes on the new plan against the current one,
  says each node restarts, and says when the new plan's disk is smaller: the nodes keep the disk
  they have, since a disk only grows.
- `list_kubernetes_versions` lists the versions a cluster can be created with, so
  `create_kubernetes_cluster` can be given a real `kubernetes_version_id`.
- `enable_instance_agent` ★ sets the Pantech agent up on a running instance with one restart, so
  nobody runs commands in the VM. The preview says the instance restarts once; only an owner or
  admin's connection can confirm it. `get_instance_agent` now says an agent is being set up while
  its one-time token is unused, and points an instance without the agent at
  `enable_instance_agent`.

### Fixed

- `create_load_balancer`'s preview prices an appliance load balancer: about what a month costs,
  estimated from the same meters it is billed on (a 1 vCPU, 1 GB VM with a 25 GB disk and a
  public IPv4). A `vpc_native` one says it costs nothing extra.
- `scale_node_pool`'s preview no longer says an initial payment is taken for each worker: workers
  are billed hourly once they join, and nothing is charged up front. When the scale adds workers,
  it says what that needs instead: a saved card, or available credit for the added workers.
  `resize_instance`'s preview no longer names the current plan's initial payment either.

## v0.3.0 — 2026-10-28

### Added

- Pantech Migrate, to plan a move from AWS. The AI app only guides: Pantech's own engine does all
  the reading, through a read-only role the user creates in their own AWS account, and no tool
  asks for or accepts cloud access keys. `connect_cloud_account` starts a connection and returns
  the one-click create-stack link and the steps (the role's template is not put in the
  conversation; it can be downloaded in the Pantech console, or with `pantech migrate connect` in
  the CLI), `set_cloud_role` sends the stack's role ARN
  and waits for Pantech's check, `retry_cloud_connection` checks again, and
  `disconnect_cloud_account` (one step: it only takes access away) removes it. Also
  `list_cloud_connections` and `get_cloud_connection`.
- `scan_cloud_account` ★ reads what the role can see and the last three months of spend; the
  preview says reading spend costs USD 0.01 on the user's AWS bill. `list_cloud_scans` and
  `get_cloud_scan` (with `include_inventory`, whose names and tags are returned as untrusted data
  only) follow it.
- `plan_migration` maps a scan to Pantech equivalents with anything that cannot move flagged and
  explained, and Pantech's monthly price next to the source bill; `list_migrations`,
  `get_migration`, `update_migration_item`, `rename_migration`, `delete_migration` ★ (needs
  "Manage and delete" access) and `export_migration_terraform` (`main.tf` and `variables.tf` for
  the Pantech Terraform provider). Copying servers is not available yet: the plan and its
  Terraform export are how a move is carried out, and the server's instructions say so.
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

- A cut file or folder listing says only its start was shown, not its end, and a failed step no
  longer claims it ran (a binary file or an ended session never runs it).
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
- When an instance's agent is not connected, `start_tools_session` and a step still queued say so,
  instead of "picks it up within seconds".
- `list_agent_session_events` with a `limit` over 500 returns 500 events, not the default 100.

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
