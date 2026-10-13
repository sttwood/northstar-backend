# Production setup and operations

This is a native Go/PostgreSQL service with a separate Next.js repository. No Docker is required. The application supports Google Workspace OIDC, authenticator MFA, SMTP, Google Drive Shared Drives, S3-compatible storage, and ClamAV. Local development may omit external providers; `APP_ENV=production` refuses to start without their configuration. Actual provider delivery and your Google tenant must be verified after credentials are installed.

## Native installation

Install PostgreSQL 16+, Go as pinned in `go.mod`, Node 20.9+ for the frontend, a reverse proxy with HTTPS, and native ClamAV (`clamd` plus regularly updated signatures). ClamAV may run on a private Linux host when developing on Windows. Never expose its TCP port publicly. Configure `StreamMaxLength` to at least 10 MB. Make PostgreSQL and S3 private.

Create a database and application role. Copy `.env.example` to the ignored `.env` and set `DATABASE_URL`, `ADMIN_EMAIL`, a unique 12–72 byte bootstrap password, and a 32-byte base64 `ENCRYPTION_KEY`. Preserve this key in a backed-up secret manager: losing it prevents decrypting authenticator secrets and queued email; changing it alone is not a supported key rotation. Bootstrap credentials initialize only an empty database. Production does not use sample data.

Build `go build -trimpath -o bin/northstar ./cmd/server` (Windows: `bin/northstar.exe`). Run it under a native service manager. It reads process environment first and `.env` second. Migrations 001/002 run transactionally on startup; take a database backup before upgrades. Multiple instances share PostgreSQL queue leases and per-company transaction locks. `/healthz` reports process availability; `/readyz` checks PostgreSQL. These endpoints do not claim external provider availability.

For a separately cloned frontend, set `NEXT_PUBLIC_DEMO_MODE=false`, `NEXT_PUBLIC_API_URL=/api`, run `npm ci`, `npm run build`, then `npm start`. Serve both behind the same HTTPS origin, proxy `/api` to Go, and set:

```dotenv
APP_ENV=production
APP_PUBLIC_URL=https://intranet.example.com
API_PUBLIC_URL=https://intranet.example.com/api
ALLOWED_ORIGINS=https://intranet.example.com
COOKIE_SECURE=true
SEED_DEMO=false
```

`API_PUBLIC_URL` includes `/api`. Use `sslmode=verify-full` and the provider CA for remote PostgreSQL. Keep service environment files, credential files, database data, and keys out of Git. A private `UPLOAD_DIR` is still used for explicit development/demo fixtures; production file bytes use S3.

Alternatively, Go can own the public entry point: set `FRONTEND_UPSTREAM_URL=http://127.0.0.1:3000` and forward all HTTPS app traffic to Go. Next.js still runs separately. Standalone native examples are included in `deploy/`. For Nexilar's current local configuration and deferred providers, see [Nexilar setup](NEXILAR_SETUP.md).

## Google Workspace sign-in and invitations

In Google Cloud, configure an internal OAuth consent screen and a Web application OAuth client for your Workspace. Add the exact callback `${API_PUBLIC_URL}/auth/sso/callback`; configure `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`. Enable the Drive API for the separate service account described below. Start the API and sign in as the initial owner using your password.

In Workspace settings → Company & delivery, set company name and exact Workspace domain, approval stages, workday hours, retention, and private Drive folder ID. Users enter the company's workspace slug on sign-in; the initial slug is `northstar`. Google verifies issuer, audience, signature, expiration, nonce, PKCE, verified email, and the signed `hd` domain claim. A matching existing active employee may link their verified identity. An otherwise new employee must have an unexpired invitation. A valid Workspace account alone does not grant access.

Invite employees in Access & people. Invitation URLs expire after seven days, are stored hashed, and are single-use. Owners/HR may copy the returned URL; SMTP sends it when configured. Invitees can create a password account or sign in with their invited Google Workspace identity. Public company/employee signup is disabled. Only existing owners can create additional companies, each with isolated employees, files, approvals and policy. Creating a company provisions that owner in the new tenant; sign in using its slug.

Security → Set up authenticator enrolls RFC 6238 TOTP and returns ten single-use recovery codes. Enrollment and disabling MFA revoke existing sessions. Company-required MFA revokes sessions and forces enrollment on next password or SSO login. Save recovery codes outside the app before continuing. A valid password/Google login alone cannot bypass required MFA. Password-reset email links expire after 30 minutes and revoke sessions; they do not disable MFA. MFA recovery is by recovery code, not password reset.

## SMTP delivery

Set `MAIL_DELIVERY=smtp`, `SMTP_ADDRESS=host:587`, `SMTP_USERNAME`, `SMTP_PASSWORD`, and `SMTP_FROM=Nexilar <noreply@company.com>`. STARTTLS is mandatory on the normal connection; for port 465 set `SMTP_IMPLICIT_TLS=true`. Certificates and hostnames are verified, with TLS 1.2 minimum. Configure your provider's verified sender, SPF, DKIM and DMARC separately.

For development only, `MAIL_DELIVERY=preview` saves private RFC email messages to `PREVIEW_EMAIL_DIR` (default `.local/mail`); no network delivery occurs. The queue records preview completion and the UI displays “Preview saved.” Preview files contain authentication links and must stay outside web roots and source control. Production explicitly rejects preview delivery. Set `MAIL_DELIVERY=disabled` to disable mail entirely. The example environment chooses local preview, so explicitly switch it to `smtp` before production activation.

Notifications and mail jobs commit in the same database transaction as request changes. Mail contains a generic request link, without salary or leave reasons. The worker runs every five seconds, retries with exponential backoff up to eight attempts, and reports queued/processing/sent/failed in Company & delivery. After correcting a provider issue, managers can use Retry delivery on a failed job. SMTP acceptance is `sent`; mailbox delivery, bounce/webhook feedback and exactly-once SMTP delivery are not asserted. A process crash after provider acceptance can cause a retry and duplicate email. Monitor failed jobs and provider bounce reports.

## Private S3 and file scanning

Set `S3_BUCKET`, `AWS_REGION`, and optionally `S3_ENDPOINT` for another S3-compatible provider. The AWS credential chain supports instance/workload roles; prefer those to permanent keys. Grant only bucket Head/List access needed for startup and object Get/Put/Delete for the application prefix. Block all public access and public ACLs; enable server-side encryption and appropriate bucket lifecycle/version policies. The app writes random tenant-prefixed keys with conditional creation. Browser downloads always pass session, tenant and employee authorization; no public/presigned file URL is returned.

Set `CLAMAV_ADDRESS=private-host:3310`. Uploads are limited to 10 MB, validate file format, and pass INSTREAM scanning before storage. Production refuses missing scanning configuration. Unavailable, malformed, infected, and missing scanner verdicts reject the upload. Drive imports are scanned too. Local development without ClamAV explicitly reports scanning disabled in delivery settings; it is not a production substitute.

## Google Drive synchronization

Create a service account, store its private JSON credential outside Git, and set `GOOGLE_APPLICATION_CREDENTIALS` to its absolute path. Share a **dedicated private Shared Drive folder** with that service account using sufficient role to create/read/delete files. Do not share this folder with all employees: employee access is provided through this application's authenticated download route. Keep one folder per company; reuse across companies is rejected.

New managed payslip/document uploads queue exports automatically. Synchronize Google Drive queues exports and paginated imports. Only PDFs inside that folder carrying this app's `northstarCompanyId`, `northstarFileId`, employee, category and pay-period app properties are imported. Arbitrary untagged files are intentionally not inferred as employee payroll. Exports use tagged file lookup to deduplicate retries; changed remote bytes import as immutable local revisions after scanning. Drive renames or remote deletion do not erase the local archive. Synchronization is an explicit action plus upload-triggered export, not a live filesystem mount. Changing an established company folder with mapped files or active sync jobs requires a planned migration; the settings API rejects an unsafe folder switch.

Retention and explicit deletion queue permanent Drive deletion and S3 object deletion after removing app metadata. Provider failure leaves a visible durable retry job. Provider versioned objects, backup copies, retention locks and legal holds require corresponding provider lifecycle/backup policies; deleting a current S3 key alone does not erase every historic object version.

## Leave, approvals and offboarding

Company work windows define paid hours and one centered unpaid break (default 09:00–18:00, eight paid hours, 13:00–14:00 break). Leave supports full day, morning/afternoon half-day, and 30-minute hourly increments on one working date. Paid fractions reserve vacation allowance and reject overlap. Approved historic duration is retained. Annual holidays can observe weekends on the next weekday, including New Year boundaries; leap-day annual holidays only occur in leap years. Working weeks are Monday–Friday; complex shifts require a separate company calendar policy.

Request approvals are owner-only. New requests snapshot one owner stage; reporting lines do not grant approval authority. Legacy pending requests may be resolved by an owner, marking remaining pending legacy stages superseded while retaining prior decisions. An owner cannot approve their own request; another active owner must review it. The paginated approval inbox includes all other employees' pending requests for owners and is empty for non-owners. Completed historic requests remain immutable. Legacy HR accounts use Employee permissions, including private salary/file access.

Offboarding revokes all sessions immediately and blocks password/SSO/MFA login. Transfer unfinished work, direct reports and outstanding review stages to an active employee. Legal holds pause automated deletion. Owners can release holds in Access & people. A daily worker applies company retention; `/api/retention/run` allows an owner-triggered run. Expired offboarded personal profiles are anonymized, HR requests and finalized payroll are removed, authenticator identities are removed, and file deletions are queued. Minimal pseudonymous employee IDs remain to preserve project references. Audit records follow their separately configured retention; backups need an independent deletion schedule. Choose retention with your company's legal obligations before enabling it.

## Thailand payroll 2026

Workspace settings → Statutory payroll targets ordinary Thai resident Section 40(1) employment income, THB, Gregorian tax year 2026. The server derives salary and approved OT from stored records and refuses duplicate finalized months. Migrating mid-year requires verified opening totals or finalized earlier months; periods before a recorded 2026 employment start are treated as no employment. Prior-employer income must be included in verified opening balances where applicable.

Rule `TH-2026-40(1)-v1` uses integer satang calculations, annual progressive rates, employment expense deduction 50% capped at THB 100,000, personal allowance THB 60,000, administrator-certified spouse/child/parent allowances, Section 33 SSO 5% with 2026 monthly base capped at THB 17,500 (THB 875 employee/employer), and configured provident contributions. SSO uses whole-baht rounding. Withholding projects remaining salary and contributions, subtracts verified YTD withholding and spreads the remaining tax across remaining periods. It does not automatically refund negative tax.

Certified other allowances are additional annual deductible amounts entered by an authorized payroll reviewer. Do not duplicate the automatic deductions; combined retirement limits, eligibility, evidence, temporary deductions, and prior-employer certificates require payroll review. This engine does not submit PND1/SSO filings, calculate non-resident/other-income tax, severance tax exemptions, tax treaties, employee loans, complex shifts or statutory benefit eligibility. Salary is the monthly amount on the profile; mid-month proration and exceptional overtime categories must be reviewed in source payroll before finalization. Finalized entries are immutable; produce a reconciled correction outside the app rather than rewriting paid history. PDF generation stores a private document linked to the ledger.

Primary rule references: [Revenue Department rates](https://www.rd.go.th/5938.html), [employment expense guidance](https://www.rd.go.th/region/12/fileadmin/163/004_pdf/guideline25650118.pdf), [Revenue Department allowance guidance](https://www.rd.go.th/fileadmin/user_upload/lorkhor/newsbanner/2025/12/PR1161_11122568.pdf), [withholding guidance](https://rd.go.th/fileadmin/tax_pdf/withhold/200360_WHT1.pdf), and [government 2026 social-security ceiling notice](https://www.doe.go.th/prd/assets/upload/files/phuket_th/a5a0263bfe7e67a4a91a5ba50f88aa8e.pdf). The allowance publication predates tax year 2026; temporary year-specific schemes are not automatically added. Have your payroll professional approve configuration and opening balances before issuing real payroll.

## Capacity, monitoring and release checks

`GET /api/v2/bootstrap` limits each initial collection to 200 records. `/api/v2/{records,employees,files,audit}` supports ID keyset cursors, limit 1–200, search, kind/status/employee/project filters; records also accept `approvals=true`. Server SQL enforces tenant and row access before applying the limit. Salary/leave privacy redaction remains enforced. `/api/statistics` computes full-database personal totals, not first-page totals. The UI explains partial lists and provides Load more. Legacy `/api/bootstrap` remains for compatibility and is unbounded; new clients should use v2.

Writes still validate a complete company aggregate under a company advisory lock. Different companies can write concurrently, but this is not a high-throughput per-employee write engine. Large read queries are paginated; load-test your expected employee count and request rate before a large-company rollout. Monitor database response time, queue depth, failed operations, scanner availability and provider errors. Project environment health remains manually reported, not a deployment probe.

Run `go test ./...` and `go vet ./...`. Set `TEST_DATABASE_URL` to a dedicated database for real migration, rollback, tenant and cursor tests. Provider protocol tests use local verified TLS/HTTP servers; they do not prove your live provider credentials. Run the frontend's `test:production` script only on a dedicated local/test deployment with external email/Drive disabled; it creates named QA companies/employees and does not delete unrelated data.

Before live release: configure your company/domain/URLs and providers, verify Google login and required MFA, deliver an invitation to a controlled mailbox, scan a clean and rejected test file, upload/download/delete a private S3 object, synchronize a managed Drive PDF, test access denial across employees/companies, reconcile a representative payroll with your payroll reviewer, and perform a database/key/object backup restore. This checkout does not provision remote infrastructure or credentials.

## Explicit core release profile

The default PRODUCTION_PROFILE=full retains all existing provider requirements. Explicit core mode permits deferred SMTP/Google/Drive while retaining production HTTPS, secure cookies, encryption and no demo seeds/previews. Without BOTH private S3 and ClamAV, all uploads return 503 and capabilities report storage/uploads disabled. Nothing silently switches to production filesystem uploads or fake delivery. See [DEPLOY_V1.md](DEPLOY_V1.md) for native Render/Postgres plus Vercel setup and the activation path.
