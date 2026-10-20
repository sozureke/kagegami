# Security policy

## Supported versions

Kagegami is pre-alpha. Only the latest commit on `main` receives fixes.

## Reporting a vulnerability

Please do not open a public issue for security problems.

Report privately through GitHub Security Advisories: open the repository's **Security** tab and choose **Report a vulnerability**.

Include what you found, how to reproduce it, and the affected version or commit. You can expect an acknowledgement within a few days.

## Scope

Of particular interest, because Kagegami processes untrusted tool output and handles sensitive engagement data:

- prompt injection through target output that makes the agent take an action or leak data
- secrets reaching logs, reports or cloud providers unmasked
- parser crashes or hangs on hostile input
- unsafe file or socket permissions under the config and data directories
