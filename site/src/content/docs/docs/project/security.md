---
title: Security
description: Reporting vulnerabilities, supported releases, and the permissions and deployment boundaries of wdx.
---

wdx runs shells and AI CLIs with your user account's permissions. It is not a sandbox, and separate worktrees do not provide security isolation. Agents default to `--permissions full`; use `--permissions ask` to leave permissions to the underlying CLI's settings.

For supported releases, private vulnerability reports, and disclosure expectations, read the project's [security policy](https://github.com/srjn45/wdx/blob/main/SECURITY.md). Please do not report vulnerabilities in public issues.

The optional hub is self-hosted. Its operator manages deployment, access, updates, backups, and retained data. See [Running a hub](/wdx/docs/guides/running-a-hub/) and [Privacy and data](/wdx/docs/project/privacy/).
