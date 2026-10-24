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
networks, firewalls, public IPs, load balancers, volumes, snapshots, managed databases and
Kubernetes clusters, price a setup, and explain your bill. See
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
| Instances | `list_instances`, `get_instance`, `create_instance` ★, `get_instance_order`, `start_instance`, `stop_instance`, `reboot_instance`, `resize_instance` ★, `rename_instance`, `rebuild_instance` ★, `restore_instance_snapshot` ★, `delete_instance` ★, `get_instance_metrics`, `get_instance_usage`, `list_instance_volumes`, `list_instance_snapshots`, `create_instance_snapshot` ★ |
| SSH | `list_ssh_keys`, `create_ssh_key` (public keys only), `delete_ssh_key` ★, `change_instance_ssh_key` ★, `open_ssh_access` ★, `get_ssh_access`, `revoke_ssh_access` |
| Networking | `list_networks`, `create_network` ★, `delete_network` ★, `list_subnets`, `create_subnet`, `delete_subnet` ★, `list_firewall_rules`, `add_firewall_rule` ★, `delete_firewall_rule` ★, `list_public_ips`, `create_public_ip` ★, `attach_public_ip` ★, `detach_public_ip`, `delete_public_ip` ★, `list_port_forwarding_rules`, `create_port_forwarding_rule` ★, `delete_port_forwarding_rule` ★, `list_security_groups`, `create_security_group`, `update_security_group_rules` ★, `delete_security_group` ★, `list_load_balancers`, `create_load_balancer` ★, `update_load_balancer` ★, `delete_load_balancer` ★ |
| Storage | `list_volumes`, `create_volume` ★, `attach_volume`, `detach_volume`, `resize_volume` ★, `delete_volume` ★, `get_volume_usage`, `list_snapshots`, `create_snapshot` ★, `restore_snapshot` ★, `delete_snapshot` ★, `get_snapshot_schedule`, `set_snapshot_schedule` ★ |
| Databases | `list_databases`, `get_database`, `create_database` ★, `get_database_order`, `start_database`, `stop_database`, `resize_database_storage` ★, `update_database_access_rules` ★, `list_database_snapshots`, `create_database_snapshot` ★, `delete_database` ★ |
| Kubernetes | `list_kubernetes_clusters`, `get_kubernetes_cluster`, `create_kubernetes_cluster` ★, `start_kubernetes_cluster`, `stop_kubernetes_cluster`, `delete_kubernetes_cluster` ★, `list_node_pools`, `create_node_pool` ★, `scale_node_pool` ★, `delete_node_pool` ★, `list_kubernetes_addons`, `toggle_kubernetes_addon` ★, `list_cluster_nodes`, `list_cluster_workloads` |
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
  console); webhook URLs are shown without query strings.
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
