# Security policy

wdx is a volunteer-maintained personal project. Security reports are welcome. This policy describes how to report a vulnerability and the security boundaries to consider when using wdx.

## Supported versions

Security fixes target the latest stable release. Older releases do not receive guaranteed backports; users should upgrade. Development releases receive best-effort attention and are not a supported security baseline.

## Report a vulnerability privately

Please do not report vulnerabilities in public issues or pull requests. Use [GitHub private vulnerability reporting](https://github.com/srjn45/wdx/security/advisories/new).

Include the affected wdx version, operating system, steps to reproduce, expected impact, and a minimal example where possible. Use synthetic data. Do not include passwords, API keys, authentication codes, private documents, or unredacted transcripts and logs.

The maintainer aims to acknowledge reports within seven calendar days, but this is not a guaranteed response time. Investigation and fixes depend on severity, reproducibility, and available time. There is no guaranteed resolution deadline or paid bug bounty.

Please allow time for investigation and coordinate public disclosure with the maintainer. When a vulnerability is addressed, the project may publish an advisory with affected versions and remediation instructions. Reporter credit will be agreed before publication.

## Security boundaries

wdx starts shells and AI CLIs with the permissions of the operating-system user running them. It is not a sandbox. Separate git worktrees keep file changes apart; they do not restrict access to other files, credentials, processes, or the network.

Agents default to `--permissions full`, which uses the supported CLI's unrestricted mode and can disable its permission prompts and sandbox. Choose `--permissions ask` to leave permissions to that CLI's settings. That setting does not itself guarantee isolation. Review the permissions and actions of every agent you run.

Terminal history, agent transcripts, logs, and project documents may contain sensitive information. Protect the wdx home directory and any backups or exports. Review and redact material before sharing it.

The hub is optional and self-hosted. Its operator is responsible for deployment, account access, updates, backups, retention, and responding to incidents on that service. Report vulnerabilities in the wdx or wdx-hub software through this policy.

Run security tests only against systems and data you own or have explicit permission to assess. Publishing this policy does not authorise testing any maintainer-operated or third-party service.

## More information

Read [Privacy and data](https://srjn45.github.io/wdx/docs/project/privacy/) for data handling and the [Apache License 2.0](LICENSE) for warranty disclaimers and limitations of liability.
