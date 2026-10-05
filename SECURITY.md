# Security

StackHarbor executes project commands with the permissions of the current user. It is not a sandbox. Inspect registrations and scripts before running an unfamiliar workspace. Keep secrets out of YAML, issue reports, and shared logs.

Report vulnerabilities privately through [GitHub private vulnerability reporting](https://github.com/szhjia/stackharbor/security/advisories/new). If that form is unavailable, open an issue requesting a private contact without publishing exploit details or credentials. Include the affected version, platform, impact, and minimal reproduction.

Security fixes target the latest published release. Older releases have no guaranteed backport support. Download binaries only from this repository's releases and verify `SHA256SUMS`; checksums detect corruption but are not an independent signature.
