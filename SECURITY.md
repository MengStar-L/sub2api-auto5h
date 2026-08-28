# Security Policy

Please report vulnerabilities privately through GitHub's security advisory feature. Do not include sub2api admin keys, setup tokens, OAuth tokens, proxy credentials, account identifiers, database files, or unredacted logs in public issues.

Supported releases receive security fixes on the latest minor version. The packaged service listens on `0.0.0.0` so a containerized reverse proxy can reach it. Restrict the application port with the host firewall and cloud security group, and expose the panel only through an authenticated SSH tunnel or an HTTPS reverse proxy.

Official Codex direct wakeup is disabled by default. When explicitly enabled, the application exports one target account's temporary OAuth and proxy material from sub2api only after quota preflight and a durable cycle lease. These secrets are used in memory for that request and must never be persisted or logged. A configured proxy failure is fail-closed and must not fall back to the host's direct network path.
