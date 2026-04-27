# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in NetSite, report it privately
through GitHub's private vulnerability reporting:

<https://github.com/shankar0123/netsite/security/advisories/new>

**Do not** open a public GitHub issue, discussion, or pull request for
security reports.

We aim to acknowledge reports within 72 hours. Coordinated disclosure
timelines are agreed on a per-report basis depending on severity, fix
complexity, and whether mitigations exist for affected deployments.

## Scope

In scope:

- The `netsite` core repository (this repo).
- Catalog repositories `netsite-providers`, `netsite-bgp-catalog`, and
  `netsite-presets` for issues that affect downstream NetSite users.

Out of scope:

- Vulnerabilities in third-party dependencies that have been reported
  upstream and have a public CVE — please file with the upstream project.
- Issues that require physical access to a deployment, social engineering
  of an operator, or a cooperating malicious tenant in a single-tenant
  install.

## Supported versions

NetSite is pre-1.0. Only the latest tagged release receives security
fixes. After 1.0, we will publish a support window policy here.
