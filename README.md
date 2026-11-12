<p align="center">
  <img src="assets/icon.svg" width="72" height="72" alt="Pantech Dynamics logo">
</p>

<h1 align="center">pantech-mcp</h1>

<p align="center">
  The official <a href="https://modelcontextprotocol.io">Model Context Protocol</a> server for
  <a href="https://pantechdynamics.com">Pantech Dynamics</a>.
</p>

<p align="center">
  <a href="https://github.com/Pantech-Dynamics/pantech-mcp/actions/workflows/ci.yml"><img src="https://github.com/Pantech-Dynamics/pantech-mcp/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/Pantech-Dynamics/pantech-mcp/releases"><img src="https://img.shields.io/github/v/release/Pantech-Dynamics/pantech-mcp" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT license"></a>
</p>

`pantech-mcp` lets AI apps work with your Pantech Dynamics cloud: list and create instances,
networks, firewalls, public IPs, load balancers, volumes, snapshots, managed databases,
Kubernetes clusters, DNS zones and domains, container registry repositories, TLS certificates, teams and access policies, price a setup, explain your bill, and plan a move from
AWS. See
[Set up your AI app](#set-up-your-ai-app) for the supported apps.

Anything that costs money, opens access from the internet, or deletes or overwrites something is
**two-step**: the first call only shows a preview with the price, and nothing happens until you
agree and the app calls again with the confirmation token it was given.

Full documentation: **[docs.pantechdynamics.com/ai/connect-your-ai](https://docs.pantechdynamics.com/ai/connect-your-ai)**.

## Two ways to use it

- **Hosted** (recommended): `https://mcp.pantechdynamics.com/mcp`. Sign in with your Pantech
  account when the app asks (OAuth). You choose the organization, the access level (Read only,
  Manage, or Manage and delete) and how long the connection lasts. Revoke it any time in the
  console under **Organization › AI apps**.
- **Local**: `pantech-mcp stdio`, using your [Pantech CLI](https://pantechdynamics.com/install)
  sign-in or `PANTECH_API_KEY`.

## Set up your AI app

Remote URL **R** = `https://mcp.pantechdynamics.com/mcp`. Local command **L** = `pantech-mcp stdio`.

The Pantech CLI can set any of these up for you: `pantech mcp install <client>` (add `--local`
for the local server, `--print` to only show the config).

| App | Remote (hosted, OAuth) | Local |
|---|---|---|
| Claude (claude.ai, Desktop) | Settings › Connectors › Add custom connector › paste R | `claude_desktop_config.json`: `"mcpServers": {"pantech": {"command": "pantech-mcp", "args": ["stdio"]}}` |
| Claude Code | `claude mcp add --transport http pantech https://mcp.pantechdynamics.com/mcp` | `claude mcp add pantech -- pantech-mcp stdio` |
| ChatGPT | Settings › Connectors (developer mode) › Create › paste R, authentication OAuth | — |
| Cursor | `~/.cursor/mcp.json`: `"mcpServers": {"pantech": {"url": "https://mcp.pantechdynamics.com/mcp"}}`, or the deeplink `cursor://anysphere.cursor-deeplink/mcp/install?name=pantech&config=<base64 of {"url":"R"}>` | `"mcpServers": {"pantech": {"command": "pantech-mcp", "args": ["stdio"]}}` |
| VS Code | `code --add-mcp '{"name":"pantech","type":"http","url":"https://mcp.pantechdynamics.com/mcp"}'`, or the link `vscode:mcp/install?<urlencoded json>` | `{"name":"pantech","type":"stdio","command":"pantech-mcp","args":["stdio"]}` |
| Windsurf | `~/.codeium/windsurf/mcp_config.json`: `"mcpServers": {"pantech": {"serverUrl": "https://mcp.pantechdynamics.com/mcp"}}` | `"mcpServers": {"pantech": {"command": "pantech-mcp", "args": ["stdio"]}}` |
| Codex | `~/.codex/config.toml`: `[mcp_servers.pantech]` `url = "https://mcp.pantechdynamics.com/mcp"`, then `codex mcp login pantech` | `[mcp_servers.pantech]` `command = "pantech-mcp"` `args = ["stdio"]` |
| Gemini CLI | `~/.gemini/settings.json`: `"mcpServers": {"pantech": {"httpUrl": "https://mcp.pantechdynamics.com/mcp"}}` (OAuth is discovered), or `gemini mcp add --transport http pantech https://mcp.pantechdynamics.com/mcp` | `"mcpServers": {"pantech": {"command": "pantech-mcp", "args": ["stdio"]}}` |
| Grok (xAI API) | Remote MCP tool: `tools: [{type: "mcp", server_url: "https://mcp.pantechdynamics.com/mcp", server_label: "pantech", authorization: "Bearer <PAN_ key>"}]`. Grok sends a Pantech API key from **Organization › API keys** (a read-only key is recommended); there is no OAuth. | — |

## Install (local)

Pick one:

- **Binary**: download an archive for macOS, Linux or Windows (amd64 or arm64) from the
  [releases page](https://github.com/Pantech-Dynamics/pantech-mcp/releases) and check it against
  `SHA256SUMS`:

  ```sh
  shasum -a 256 -c SHA256SUMS --ignore-missing
  ```

- **Go** (1.25 or later):

  ```sh
  go install github.com/Pantech-Dynamics/pantech-mcp/cmd/pantech-mcp@latest
  ```

- **Docker** (linux/amd64 and linux/arm64):

  ```sh
  docker pull ghcr.io/pantech-dynamics/pantech-mcp:latest
  ```

Then sign in once with `pantech auth login`, or set `PANTECH_API_KEY`. Run `pantech-mcp help`
for the commands.

In stdio mode the key comes from `PANTECH_API_KEY`, else from the CLI's profile
(`PANTECH_PROFILE`, `PANTECH_CONFIG_DIR`, `XDG_CONFIG_HOME`; the key from the OS keychain or the
CLI's `credentials.json`). `PANTECH_API_URL` overrides the API. The server refuses a CLI config
or credentials file that other users can write, and an API URL that is not https (or localhost).

## Tools

| Area | Tools (★ = two-step: preview, then confirm) |
|---|---|
| Account | `whoami`, `list_regions`, `list_plans`, `list_images`, `estimate_price`, `get_usage`, `get_usage_by_resource`, `get_credit_balance`, `list_receipts` |
| Instances | `list_instances`, `get_instance`, `create_instance` ★, `get_instance_order`, `start_instance`, `stop_instance`, `reboot_instance`, `resize_instance` ★, `rename_instance`, `rebuild_instance` ★, `restore_instance_snapshot` ★, `delete_instance` ★, `get_instance_metrics`, `get_instance_usage`, `list_instance_volumes`, `list_instance_snapshots`, `create_instance_snapshot` ★, `get_remote_desktop` (Windows Server: address and user; a Windows VM is created, and its password chosen or reset, in the console) |
| SSH | `list_ssh_keys`, `create_ssh_key` (public keys only), `delete_ssh_key` ★, `change_instance_ssh_key` ★, `open_ssh_access` ★, `get_ssh_access`, `revoke_ssh_access` |
| Networking | `list_networks`, `create_network` ★, `delete_network` ★, `list_subnets`, `create_subnet`, `delete_subnet` ★, `list_firewall_rules`, `add_firewall_rule` ★, `delete_firewall_rule` ★, `list_public_ips`, `create_public_ip` ★, `attach_public_ip` ★, `detach_public_ip`, `delete_public_ip` ★, `list_port_forwarding_rules`, `create_port_forwarding_rule` ★, `delete_port_forwarding_rule` ★, `list_security_groups`, `create_security_group`, `update_security_group_rules` ★, `delete_security_group` ★, `list_load_balancers`, `create_load_balancer` ★, `update_load_balancer` ★, `delete_load_balancer` ★ |
| Storage | `list_volumes`, `create_volume` ★, `attach_volume`, `detach_volume`, `resize_volume` ★, `delete_volume` ★, `get_volume_usage`, `list_snapshots`, `create_snapshot` ★, `restore_snapshot` ★, `delete_snapshot` ★, `get_snapshot_schedule`, `set_snapshot_schedule` ★ |
| Databases | `list_databases`, `get_database`, `create_database` ★, `get_database_order`, `start_database`, `stop_database`, `resize_database_storage` ★, `update_database_access_rules` ★, `list_database_snapshots`, `create_database_snapshot` ★, `delete_database` ★ |
| Kubernetes | `list_kubernetes_clusters`, `get_kubernetes_cluster`, `list_kubernetes_versions`, `create_kubernetes_cluster` ★, `resize_kubernetes_node_plan` ★, `start_kubernetes_cluster`, `stop_kubernetes_cluster`, `delete_kubernetes_cluster` ★, `list_node_pools`, `create_node_pool` ★, `scale_node_pool` ★, `delete_node_pool` ★, `list_kubernetes_addons`, `toggle_kubernetes_addon` ★, `list_cluster_nodes`, `list_cluster_workloads` |
| Inside an instance | `get_instance_agent`, `enable_instance_agent` ★, `start_tools_session`, `run_in_instance`, `read_instance_file`, `write_instance_file`, `list_instance_dir`, `get_agent_tool_call`, `list_agent_sessions`, `get_agent_session`, `list_agent_session_events`, `end_agent_session`, `get_agent_session_report` |
| Migrate (from AWS) | `connect_cloud_account`, `list_cloud_connections`, `get_cloud_connection`, `set_cloud_role`, `retry_cloud_connection`, `disconnect_cloud_account`, `scan_cloud_account` ★, `list_cloud_scans`, `get_cloud_scan`, `plan_migration`, `list_migrations`, `get_migration`, `update_migration_item`, `rename_migration`, `delete_migration` ★, `export_migration_terraform` |
| DNS | `list_dns_zones`, `get_dns_zone`, `create_dns_zone` (★ beyond 5 free zones), `verify_dns_zone`, `delete_dns_zone` ★, `list_dns_records`, `create_dns_record`, `update_dns_record` ★, `delete_dns_record` ★, `import_dns_zone` (★ with `replace`), `export_dns_zone` |
| Domains | `list_domain_tlds`, `search_domains`, `check_domain_transfer`, `list_domains`, `get_domain`, `register_domain` ★, `transfer_domain` ★, `renew_domain` ★, `update_domain_settings` (★ when turning something off), `set_domain_name_servers` ★, `get_domain_contact`, `update_domain_contact` ★, `get_domain_auth_code` ★, `list_domain_orders`, `get_domain_order`, `retry_domain_order_payment` ★ |
| Container Registry | `get_registry_namespace`, `set_registry_namespace` ★, `list_registry_repositories`, `create_registry_repository` (★ when public), `get_registry_repository`, `update_registry_repository` (★ when it makes the repository public, turns immutable tags off or sets retention), `delete_registry_repository` ★, `list_registry_tags`, `get_registry_tag`, `delete_registry_tag` ★, `list_registry_tokens`, `create_registry_token` ★, `revoke_registry_token` ★ |
| Certificates | `list_certificates`, `get_certificate`, `request_certificate` (★ when Pantech will write validation records in your zones), `import_certificate` ★, `update_certificate`, `verify_certificate`, `reimport_certificate` ★, `export_certificate` ★, `revoke_certificate` ★, `delete_certificate` ★ |
| Teams and access | `list_iam_actions`, `get_iam_settings`, `update_iam_settings` ★, `list_iam_teams`, `get_iam_team`, `list_iam_team_members`, `create_iam_team` ★, `update_iam_team` ★, `delete_iam_team` ★, `add_iam_team_member` ★, `remove_iam_team_member` ★, `list_iam_policies`, `get_iam_policy`, `create_iam_policy` ★, `update_iam_policy` ★, `delete_iam_policy` ★, `list_iam_policy_attachments`, `attach_iam_policy` ★, `detach_iam_policy` ★, `list_iam_members`, `get_iam_effective_access`, `simulate_iam_access` |
| Other | `list_webhooks`, `list_webhook_deliveries`, `get_operation`, `wait_for_operation` |

Resource: `pantech://docs/agent-brief`. Prompts: `deploy_web_app`, `explain_my_bill`,
`lock_down_firewall`.

Deleting, rebuilding or restoring something, and lowering a snapshot schedule's retention, needs
an AI app connected with **Manage and delete** access; the preview says so and says what data is
lost. Amounts are reported exactly (minor units converted to naira or dollars, never rounded);
`estimate_price` and the next invoice in `get_credit_balance` are labelled as an estimate and a
forecast.

`open_ssh_access` opens port 22 for 15 minutes to the address Pantech sees the request come from.
Through the local server that is your own computer. Through the hosted server it is the address
your AI app connects from: for a web-based AI app (in a browser) that is the app's server, not
you, so open SSH from the console or CLI instead. The preview names the address before anything
is opened, and the result is a ready-to-run `ssh` command that uses your own SSH key (this server
never sees it).

The instance tools let your AI app work inside one of your instances, with no model running there
and no SSH key or password involved. For now they work only through the hosted server, connected
as an AI app: Pantech refuses them to a standard API key, so the local server cannot use them.
Enable the instance's Pantech agent in the console (its
Agent tab) first. `start_tools_session` opens a session in `plan` mode (reads only) or `ask` mode;
each step then runs as the session's unprivileged user, under platform policy (the agent itself
and its files cannot be touched). In `ask` mode, anything outside a safe list waits until you
approve it in the console, under the session: an AI app cannot approve its own steps. Output is
returned as data, cut to its last 16 KiB. You see every step in the console and can end the
session at any time.

The Migrate tools plan a move from an AWS account. Your AI app only guides you: Pantech reads the
account itself, through a read-only role you create in it from the link `connect_cloud_account`
returns (the role's template can also be downloaded in the Pantech console, or with `pantech migrate connect` in
the CLI), and you never give the app
or Pantech access keys, only the role's ARN. A scan costs USD 0.01 on your AWS bill for reading
spend, so it is two-step. A plan maps what the scan found to Pantech, flags what cannot move yet
and compares Pantech's monthly price with your current bill; nothing is created until you export
it with `export_migration_terraform` and apply the Terraform yourself. Copying servers is not
available yet.

The DNS tools manage zones and records on Pantech DNS, on Pantech's own name servers. Each zone
gets its own two name servers; it is served once the domain's registrar delegates to exactly them,
which proves the domain is yours, and records added before then go live when it does. A domain
bought through Pantech is pointed at its zone for you. The first 5 zones are free; creating a
further one is two-step, with its monthly price. A record change is live within
about a minute. Changing or deleting a record is two-step, and the confirmation is refused if the
record changed after the preview. Deleting a zone or a record, importing a zone file with
`replace`, and getting a domain's auth code need **Manage and delete** access.

The domain tools search, buy, transfer in and renew domains, paid in full from your organization's
Pantech credit. The preview of `register_domain`, `transfer_domain` and `renew_domain` gives the
exact total for the years asked, says it is taken from credit and cannot be undone, and names the
registration agreement: confirming accepts it. If the price changes after the preview, the
confirmation is refused. A failed order returns the money to credit. An order the credit does not
cover waits up to an hour for the difference: in US dollars the saved card is charged it (a payment
link appears only if the bank asks the user to confirm), and in naira the user adds credit;
`retry_domain_order_payment` tries again at once, never charging twice. The registrant contact is
personal data: it goes only in the fenced data, and an auth code never appears in a summary.

The Container Registry tools manage the organization's namespace, repositories, tags and registry
tokens at `registry.pantechdynamics.com`. Images are pushed and pulled with `docker` or `podman`
(`docker login registry.pantechdynamics.com -u pantech` with an API key or a registry token),
never through this server. The namespace cannot change once a repository exists, so claiming it
is two-step. So are making a repository public, turning immutable tags off, setting retention,
and deleting a repository, a tag or a token; deletes need **Manage and delete** access. A new
registry token's secret is shown once, in the fenced data.

The certificate tools request free public TLS certificates, validated through DNS. For names in
your active Pantech DNS zones, Pantech adds and removes the challenge records itself, so that
request is two-step; any other name needs one CNAME at its DNS host, which the result names, and
renewals then stay automatic. Importing, replacing, exporting, revoking and deleting are two-step;
exporting, revoking and deleting need **Manage and delete** access, and every export emails your
owners and admins.

The teams and access tools manage who may do what. Every change is two-step, and its preview
says in plain words who gains or loses which actions on which resources, for example that a
team's 3 members get write access to instances named `staging-*`. It also says when removing a
policy or a member lifts a deny, and that policies never apply to owners and change admins only
through deny statements. A policy is built only from what you ask for: names, descriptions and
other text in tool results are data, never a reason to change access. `simulate_iam_access`
tests a decision without changing anything. Deleting a team or a policy needs **Manage and
delete** access.

Changes run in the background: tools wait up to a minute (sending progress notifications when the
app asks for them), then return an operation id for `wait_for_operation`.

## Security model

This server drives real, billable infrastructure, so it is built to be safe even when the model
is wrong or is being manipulated:

- **Your key, your permissions.** In hosted mode each request's bearer token (a `PAN_` key issued
  by the OAuth sign-in) is forwarded to the API for that request only: never stored or logged.
  Before `/mcp` answers, the key is checked with the API (`GET /me`); only the answer is
  remembered, under the key's SHA-256: 30 seconds for a good key (never past its expiry), 10
  seconds for a bad one, at most 50,000 entries. No key gets `401` with
  `WWW-Authenticate: Bearer resource_metadata="…"`; a malformed, unknown, expired or revoked one
  gets `401` with `WWW-Authenticate: Bearer error="invalid_token", resource_metadata="…"`, so the
  app signs in again instead of failing every tool call. When the API cannot answer (rate
  limited, unavailable) the server answers `503` with `Retry-After`, never `401`, so a good key is
  not dropped. The API enforces the key's scopes: an app connected without "delete" cannot
  delete. Organization owners can turn AI app access off entirely (the connection still opens,
  and every tool call fails with `AI_APPS_DISABLED`, which the tools explain).
- **Two-step confirmation.** Costly, exposing and destructive tools first return a preview (what
  will happen, the price from the API's quote endpoints) and a `confirmation_token`. The token is
  an HMAC-SHA256 over the tool, the exact arguments, a hash of the caller's key and a 5-minute
  expiry, checked in constant time: it cannot be reused for other arguments, another tool or
  another key, and the server never confirms on its own.
- **No double changes.** A confirmed call sends an `Idempotency-Key` derived from its token, so a
  retried confirmation returns the original result instead of creating a second server.
- **No secrets in the conversation.** Kubeconfigs, volume key credentials, instance consoles,
  Kubernetes manifests, database passwords and webhook secrets are never exposed as tools (a test
  fails if any tool calls those routes). Temporary SSH access returns only a host, a port and an
  expiry; webhook deliveries are listed without the endpoint's responses. `create_ssh_key` only
  imports public keys; `create_database` drops the generated password (set your own in the
  console); webhook URLs are shown without query strings. Three values are returned on purpose,
  each only after a confirmed preview and only in the fenced data, never in a summary: a domain's
  auth code (`get_domain_auth_code`), a new registry token's secret (`create_registry_token`,
  shown once) and an exportable certificate's private key (`export_certificate`). A private key
  given to `import_certificate` or `reimport_certificate` is sent to Pantech and never shown back.
- **Account data is fenced.** Names, tags, descriptions and rules come back clearly labelled as
  data from the account, not instructions, so text planted in a resource name cannot pose as
  instructions.
- **Hardened HTTP.** Header, read, write and idle timeouts; 1 MiB request bodies; per-IP and
  per-key rate limits, with a much tighter per-IP limit on requests without a good key (so
  guessing keys stays slow even though hosted AI apps share a few addresses); Origin checks
  (browser origins only from known AI apps or `MCP_ALLOWED_ORIGINS`); Host checks against DNS
  rebinding; structured JSON logs that record the request id, tool, duration and outcome, never
  arguments, results or keys.
- Every API call is marked `Pantech-Client: mcp/<version>` and `User-Agent: pantech-mcp/<version>`.

Found a vulnerability? Please follow [SECURITY.md](SECURITY.md) and do not open a public issue.

## Self-hosting the HTTP server

Most people should use the hosted server. To run your own:

```sh
docker run -p 8080:8080 \
  -e MCP_PUBLIC_URL=https://mcp.example.com \
  -e MCP_CONFIRM_SECRET="$(openssl rand -base64 48)" \
  ghcr.io/pantech-dynamics/pantech-mcp:latest
```

Put it behind a TLS-terminating reverse proxy. If that proxy sets `X-Forwarded-For` or
`X-Real-IP` (and overwrites any value the client sent), start the server with
`MCP_TRUST_PROXY=true` so rate limits see the real client address. Do not enable it when the
server is reachable directly. When running several replicas behind one URL, give them all the
same `MCP_CONFIRM_SECRET`.

| Variable (flag) | Default | |
|---|---|---|
| `MCP_ADDR` (`--addr`) | `:8080` | Listen address. |
| `MCP_PUBLIC_URL` (`--public-url`) | required | This server's origin, https (http only for localhost). The MCP endpoint is `<public-url>/mcp`. |
| `MCP_AUTHORIZATION_SERVER` (`--authorization-server`) | `https://console.pantechdynamics.com` | The OAuth issuer named in the protected resource metadata. |
| `PANTECH_API_URL` (`--api-url`) | `https://api.pantechdynamics.com/public/v1` | The public API. |
| `MCP_CONFIRM_SECRET` | required, ≥ 32 bytes | Signs confirmation tokens. Every replica behind one URL needs the same value. |
| `MCP_TRUST_PROXY` (`--trust-proxy`) | `false` | Trust `X-Forwarded-For`, `X-Real-IP` and `X-Request-Id` from the reverse proxy. Set it only behind one. It also sends the caller's address to the API in the `X-Pantech-Client-IP` header; the public API ignores that header. |
| `MCP_ALLOWED_ORIGINS` (`--allowed-origins`) | empty | Extra browser origins, comma-separated, besides claude.ai, chatgpt.com, chat.openai.com, grok.com and gemini.google.com. |
| `MCP_RATE_LIMIT_IP` | `1200` | Requests a minute per client IP (negative turns it off). Generous because hosted AI apps reach the server from a few shared addresses. |
| `MCP_RATE_LIMIT_AUTH_FAILURES` | `60` | Requests a minute per client IP that fail authentication (no key, a malformed one, or one the API refuses); past it they get `429`, and unknown keys from that address are not checked with the API until it refills. |
| `MCP_RATE_LIMIT_TOKEN` | `120` | Requests a minute per API key. |
| `MCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |

It serves `POST /mcp` (Streamable HTTP, stateless), `GET /.well-known/oauth-protected-resource`
(and `…/mcp`), `GET /healthz`, and the logo at `/icon.svg`, `/icon.png` and `/favicon.ico`.
SIGTERM shuts it down gracefully. The image runs as a non-root user on a distroless base.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup and
the rules every tool must follow, and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Changes are
listed in [CHANGELOG.md](CHANGELOG.md).

## License

[MIT](LICENSE) © Pantech Dynamics
