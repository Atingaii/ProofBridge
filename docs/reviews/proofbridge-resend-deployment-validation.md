# Resend sending-domain and application validation

Date: 2026-11-07. This report contains no mailbox address, token, or private environment values.

## Configured resources

- Dedicated sending domain: `proofbridge-mail.sparkon.cn`, Resend region `ap-northeast-1`.
- Sending enabled; receiving, open tracking and click tracking disabled.
- Resend reports the domain and its four required records `verified`.
- Cloudflare had 15 DNS records before setup. Four new records were added; all original records remained byte-for-byte identical in the API snapshots.
- Added TXT `resend._domainkey.proofbridge-mail`, MX and TXT `send.proofbridge-mail`, and DNS-only CNAME `rsend.proofbridge-mail` within `sparkon.cn`. Values came directly from Resend, not copied from a generic example.
- Created a dedicated `sending_access` key restricted to this domain. Private values are outside Git, mode `0600`.

## Actual application SMTP test

A separate Hub process with a new isolated database loaded the real SMTP configuration. A temporary account used the current user's saved recipient address in memory. Exactly one request to `POST /api/automation/mail/test` returned `200`, `smtpAccepted=true`, `deliveryMode=smtp` between 02:08:25 and 02:08:36 UTC.

Resend list and retrieve operations matched the same unique message by recipient, fixed subject and timestamp; both reported `delivered`. This proves acceptance by the recipient's mail server. No claim is made about inbox visibility, spam placement or rendering in the recipient's email client.

The real account preferences, database, live Hub, Bridge and ArraySet workers were unchanged. Temporary Hub, database, binaries, scripts and private copies were removed. Detailed redacted evidence is in `.trellis/tasks/11-06-mcp-run-detail-selection/validation/resend-smtp-application-test.md`.

## Live activation and deployment

The initial SMTP test ran while the live Hub remained on `preview`. After all three proof runs stopped, the local Hub was backed up and updated to `371577c-local`; its private environment was switched to real Resend SMTP. The Bridge was not restarted.

The updated live site's `get_context` reports `mailDeliveryMode=smtp` and ready mail. One real authenticated `POST /api/automation/mail/test` then returned SMTP acceptance; Resend list and retrieve operations confirmed that same message as `delivered`. Both client configuration panels and their AI setup prompts were checked against the actual Hub response, including a narrow viewport. Account preferences remained unchanged.

A separate, clearly labeled HTML style test used real recorded statistics from a completed task. Both HTML and plain text matched the reported Token/cost values and preserved incomplete-accounting and estimate labels. Its recipient server also accepted delivery. No complete conversation or long model conclusion was included.

The final source commit also passed a pure `git archive` deployment check with 28 assertions. Production multi-stage Docker image construction remained unverified because the external registry timed out; isolated Linux nonroot runtime and fresh-volume checks passed separately. These checks do not establish deployment to a laboratory server or VPN reachability.

The portable setup contract and shared environment template are described in [repository deployment parity](../deployment.md#repository-deployment-parity). The Cloudflare/Resend plugins provision infrastructure; the deployed Hub uses standard TLS SMTP and requires neither plugin at runtime. Keep the private environment, persistent SQLite database and original JWT secret outside Git.
