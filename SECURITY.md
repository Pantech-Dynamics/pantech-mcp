# Security policy

`pantech-mcp` lets AI apps manage real, billable infrastructure, so we take its security
seriously and appreciate responsible disclosure.

## Reporting a vulnerability

**Please do not open a public issue, discussion or pull request for a security problem.**

Email **info@pantechdynamics.com** with:

- a description of the issue and its impact,
- steps to reproduce (a proof of concept if you have one),
- the version (`pantech-mcp version`) and mode (stdio, self-hosted http, or the hosted server).

You can also use GitHub's [private vulnerability reporting](https://github.com/Pantech-Dynamics/pantech-mcp/security/advisories/new).

We aim to acknowledge reports within 3 business days and to keep you updated until the issue is
resolved. We will credit you in the advisory unless you prefer otherwise. Please give us a
reasonable time to fix the issue before disclosing it, and do not access or change data that is
not yours while testing.

Examples of what we want to hear about: a way to run a costly, exposing or destructive tool
without a valid confirmation, to reuse a confirmation token for other arguments, tools or keys,
to get a secret into a tool result, to bypass the Origin or Host checks, or to make the server
store or log a key.

## Supported versions

Security fixes are released for the latest minor version. Please upgrade to the newest release
before reporting.

| Version | Supported |
|---|---|
| 0.1.x (latest) | Yes |
| Older | No |

The hosted server at `https://mcp.pantechdynamics.com/mcp` always runs a supported version.
