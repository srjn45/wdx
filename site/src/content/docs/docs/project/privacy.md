---
title: Privacy and data
description: What wdx stores locally, which network services it uses, and what an optional self-hosted hub receives.
---

## Scope

wdx is software you run on your own machines. The project does not provide a public hosted hub. This page describes data handling by wdx, its documentation website, and an optional hub you choose to operate or join. The AI services and hosting providers you use have their own terms and privacy information.

## Data on your machine

wdx keeps its database, configuration, logs, terminal history, agent conversation copies, and other working data in its home directory, normally `~/.wdx`. `WDX_HOME` can select another directory. AI CLIs may also keep their own credentials, settings, and conversations elsewhere.

Terminal history and agent transcripts can contain prompts, source code, command output, file paths, and secrets printed during a session. They are separate from diagnostic logs. Protect the wdx directory using your operating system's access controls and disk protection; do not assume it is an encrypted vault.

Agent transcripts remain until the agent is removed, then move to a local trash directory with a seven-day retention period. The daemon removes expired copies while it is running; stopping it or a cleanup error can delay removal. Diagnostic log retention is controlled by `log.retention_days`, whose default is seven days besides today's file. Terminal history is limited by `terminal.history_lines`. Other records have their own lifecycle; removing an agent or uninstalling wdx does not promise that all copies have been erased from backups, git, AI CLIs, or a hub.

## AI services and permissions

wdx launches the supported AI CLIs installed on your machine, including their own authentication and network connections. Those CLIs may send prompts, code, files, tool output, and other context to their providers according to their settings and applicable terms. Local execution of wdx does not mean that AI processing stays on your machine.

wdx also reads provider-specific local data to show conversations and usage. For Cursor's usage statistics, wdx queries Cursor's API using the CLI's login and keeps the usage response locally with the agent's backup.

You choose the provider, account, permissions, and material made available to an agent. Review your provider's data controls and your organisation's policies before using confidential information.

## Logs and metrics

wdx writes diagnostic logs locally. Logs can include operational information such as names, paths, errors, and identifiers. They are not the same as terminal history or conversation copies. Review all logs and exports before sharing them.

The daemon sends operational metrics to `otel.endpoint`, which defaults to `http://localhost:4318`. This is a collector on your own machine, not a project-operated analytics endpoint. Setting the endpoint to an empty value disables metric export. Configuring another endpoint sends metrics to that collector, whose operator determines further storage and access. The optional observability stack can also read local diagnostic logs.

## Network requests

Installing and updating wdx downloads software from GitHub or the release source you configure. Starting the optional observability stack can download its container image through Docker or Podman. Git operations and GitHub features use the remotes and services involved in those operations. AI CLIs make their own requests, and wdx queries Cursor for usage statistics as described above. An optional hub connects to the address you select. Those services can receive network metadata, such as your IP address, as well as the data needed for the operation.

## Optional self-hosted hub

Using wdx locally does not require a hub. To join one, choose its address with `wdx hub login --url https://hub.example.com` and obtain access from its operator.

A hub processes account and authentication records, node identities and connection information, and security/audit records. Enabling project document sync uploads the project's documents and their revisions and metadata, including its repository identity. Plans are not uploaded by document sync.

Stored document revisions are encrypted under keys managed by the hub. This is not end-to-end encryption against its operator. Remote node sessions use a separate encrypted connection between nodes; the hub relays that traffic and still observes connection and traffic metadata.

Turning document sync off stops future uploads and discards pending changes; it does not erase material already stored on the hub. Logging out or removing a local project is also not a request to erase the hub's stored data. Ask its operator about access, retention, deletion, backups, and any applicable privacy notice or data-processing agreement before uploading sensitive material.

## This website and GitHub

The documentation website is hosted on GitHub Pages. GitHub may process request information to deliver and protect the site; see its [privacy statement](https://docs.github.com/en/site-policy/privacy-policies/github-general-privacy-statement). The site serves its fonts locally and stores your selected light or dark theme in your browser's local storage. The documentation sidebar also remembers expanded sections using browser session storage. The project does not add advertising or audience-tracking analytics to the site.

The interactive TIDE demonstration uses example data and does not run agents or connect to your repositories. Following an external link takes you to that service, which has its own privacy information. Viewing the repository README can also load badges from external badge services.

Contributions, issues, and pull requests on GitHub are public. For questions about this description, open an issue without including private data. For vulnerabilities, use the private route in the [security policy](https://github.com/srjn45/wdx/blob/main/SECURITY.md). For data held by your hub or AI provider, contact that operator or provider.
