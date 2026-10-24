# Contributing to pantech-mcp

Thanks for helping improve the Pantech Dynamics MCP server. This guide covers the development
setup, the tests, how tools map to the public API, and the security rules every change must keep.

By taking part you agree to follow our [Code of Conduct](CODE_OF_CONDUCT.md). Please report
security issues privately as described in [SECURITY.md](SECURITY.md), never in a public issue or
pull request.

## Development setup

You need Go 1.25 or later, `make`, and optionally Docker.

```sh
git clone https://github.com/Pantech-Dynamics/pantech-mcp
cd pantech-mcp
make test     # gofmt -l . (must print nothing), go vet ./..., go test -race ./...
make build    # ./pantech-mcp for this machine
make dist     # every release platform still builds (archives and SHA256SUMS in ./dist/)
make docker   # the container image
```

Try it locally with an API key from the console (a read-only key is a good idea while
developing):

```sh
PANTECH_API_KEY=PAN_... ./pantech-mcp stdio
```

The [MCP Inspector](https://github.com/modelcontextprotocol/inspector) is handy for calling tools
by hand.

### Building before the Go SDK is published

The server depends on [`pantech-go`](https://github.com/Pantech-Dynamics/pantech-go), the Pantech
Go SDK. Until the version named in `go.mod` is published, `go.sum` has no entry for it and a
plain `go build` fails. In that case clone `pantech-go` next to this repository and point Go at it
with a workspace file kept **outside** this repository:

```
go 1.25.0
use /path/to/pantech-mcp
use /path/to/pantech-go
replace github.com/Pantech-Dynamics/pantech-go v0.1.0 => /path/to/pantech-go
```

```sh
GOWORK=/path/outside/the/repo/go.work make test
```

For Docker, pass the checkout as a build context:
`docker buildx build --build-context pantech-go=/path/to/pantech-go -t pantech-mcp .`

CI does the same automatically. Releases are blocked until `go.sum` is complete, so the SDK must
be published (and `go get github.com/Pantech-Dynamics/pantech-go@<version> && go mod tidy` run
here) before the first release. **Never commit a `go.work` file or a `replace` directive.**

## Layout

```
cmd/pantech-mcp/       main: stdio, http, version, help
mcp/server.go          New(Options): the server, its identity and icons, the per-call API client, call logging
mcp/safety.go          Confirmer: HMAC confirmation tokens, Idempotency-Key derivation, Subject
mcp/auth.go            bearer check (PAN_ format, then GET /me, cached by hash), requireKey, RFC 9728 metadata
mcp/http.go            http mode: routes, Origin/Host checks, CORS, body cap, rate limits, timeouts, icons
mcp/ratelimit.go       in-memory token buckets
mcp/resources.go       pantech://docs/agent-brief
mcp/prompts.go         deploy_web_app, explain_my_bill, lock_down_firewall
mcp/tools/kit.go       Spec, Risk, add / addConfirmed (the two-step flow), Env, Call
mcp/tools/result.go    Result rendering (untrusted-data fence), error mapping, money
mcp/tools/wait.go      waiting on operations with progress notifications
mcp/tools/excluded.go  routes no tool may call
mcp/tools/<area>.go    the tools, one file per resource family
internal/cliconfig/    reads (never writes) the Pantech CLI's profile and key
assets/                the logo (embedded)
```

## How tools map to the public API

Every tool is a thin, safe wrapper over one or more operations of the Pantech public API, through
the `pantech-go` SDK. The API is described by its OpenAPI document:
<https://docs.pantechdynamics.com/openapi/public-api.yaml>.

When you add or change a tool:

1. Find the operation(s) in the OpenAPI document and use the matching `pantech-go` method.
2. List every route the tool calls in `Spec.Routes`. `TestEveryToolCallsOnlyItsRoutes` calls each
   tool against a fake API and fails on an unlisted or excluded route.
3. Pick the tool's `Spec.Risk` and add it to `wantRisk` in `mcp/tools/registry_test.go`.
4. Write the description for a model: what it does, when to use it, and what it costs.
5. Add a line to `CHANGELOG.md` under `## Unreleased`.

## Security expectations

This server lets AI models drive real, billable infrastructure, and models can be wrong or
manipulated (prompt injection through resource names, web pages, documents). Every change must
keep these rules; reviews check them first.

- **Confirmation for anything costly or destructive.** `Spec.Risk` is `Read`, `Write`, `Cost`
  (costs money), `Exposure` (opens access from the internet) or `Destructive` (deletes, or
  overwrites data or a whole rule list). `Cost` and above must be registered with `addConfirmed`,
  which previews first (with the price from the API's quote or estimate endpoints where one
  exists) and executes only with a valid confirmation token. Tool annotations (readOnlyHint,
  destructiveHint, idempotentHint, openWorldHint) come from the spec.
- **The preview never writes.** A preview may only make GET requests (and the read-only
  `POST /pricing/estimate`); the end-to-end test checks it.
- **One write per confirmed execution**, sent with `c.writeOpts()` so it carries the
  Idempotency-Key derived from the token.
- **Never return or accept secrets.** No tool may expose kubeconfigs, volume key credentials,
  instance consoles, SSH access grants, database passwords, webhook creation or secret rotation,
  credit purchases or account deletion. `excluded.go` and `TestNoExcludedCalls` enforce this.
  Decode responses that may carry a secret into a struct without that field (see
  `create_ssh_key`, `create_database`).
- **Fence untrusted data.** `Result.Text` holds only our own words and API-generated ids.
  Anything a customer chose (names, tags, descriptions, rules, URLs) goes in `Result.Data`, which
  is shown fenced as untrusted data, so planted text cannot pose as instructions.
- **Validate path ids** with `checkIDs` before they reach a URL.
- **Safe errors.** API errors show the API's title, detail and request id (`toolError`); anything
  else is a generic message. Never echo `err.Error()` of a non-API error.
- **Never log** keys, request bodies, tool arguments or results. Logs are slog JSON: request id,
  tool, duration, outcome, phase, error code, API request id.
- **The bearer is per request.** In http mode the key lives in the request's TokenInfo for that
  request only: never store or cache it, and never read `PANTECH_API_KEY` in http mode. The
  verification cache in `auth.go` keys on the key's SHA-256 and holds only the verdict.
- **Neutral customer-facing text.** Tool descriptions, results and errors describe Pantech
  products only; they do not name third-party providers.

## Pull requests

- Keep each pull request focused, and explain what changes for users.
- `make test` and `make dist` must pass; CI runs both and builds the Docker image.
- Add or update tests with every behavior change.
- Note user-visible changes in `CHANGELOG.md` under `## Unreleased`.
- Dependencies are kept up to date by Dependabot; avoid adding new ones without discussion.

## Releasing (maintainers)

1. In `CHANGELOG.md`, rename `## Unreleased` to `## vX.Y.Z — YYYY-MM-DD` (with the `v`), and bump
   the default `Version` in `mcp/server.go` to match.
2. Merge to `main`. `auto-release.yml` tags the new version and runs `release.yml`, which
   builds the binaries and `SHA256SUMS` (`make dist`), publishes them as a GitHub release with the
   changelog section as notes, and pushes `ghcr.io/pantech-dynamics/pantech-mcp:<version>` and
   `:latest` for linux/amd64 and linux/arm64.

Pushing a `vX.Y.Z` tag by hand releases the same way; a tag with a hyphen (`v0.2.0-rc.1`) is
published as a pre-release and does not move `latest`. The binary's version comes from the tag
through `-ldflags`.
