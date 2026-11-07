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

At this checkpoint, the live Hub remains on explicit `preview` while three proof runs are active. A private real-SMTP environment is prepared for activation after those runs are idle. Domain verification and the successful isolated SMTP test do not establish that the live Hub has already switched.

The portable setup contract and shared environment template are described in [repository deployment parity](../deployment.md#repository-deployment-parity). The Cloudflare/Resend plugins provision infrastructure; the deployed Hub uses standard TLS SMTP and requires neither plugin at runtime. Keep the private environment, persistent SQLite database and original JWT secret outside Git.
