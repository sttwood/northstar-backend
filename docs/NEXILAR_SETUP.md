# Nexilar setup

Company: **Nexilar**. Payroll scope: Thailand, THB, Gregorian tax year 2026. Employees join by invitation. No Workspace domain or public hostname has been supplied; leave those provider fields empty until available. A GitHub repository URL stores source code and is not a running frontend/API URL.

## Run locally without external providers

From the parent workspace, run `npm run setup`, `npm run db:setup`, then `npm run dev`. Open **http://localhost:8080**. The backend owns this entry point and proxies pages/assets to the independently running frontend on port 3000. Go still handles `/api`, `/healthz` and `/readyz`. Use company workspace `northstar` and the owner credentials in ignored `backend/.env`. The display name is Nexilar. Existing installations can change their company name in Workspace settings; `COMPANY_NAME` only initializes an empty database.

For independent clones, create PostgreSQL and set backend `.env` from its example. Set `COMPANY_NAME=Nexilar`, `APP_PUBLIC_URL=http://localhost:8080`, `API_PUBLIC_URL=http://localhost:8080/api`, `FRONTEND_UPSTREAM_URL=http://127.0.0.1:3000`, `ALLOWED_ORIGINS=http://localhost:3000,http://localhost:8080`, and `MAIL_DELIVERY=preview`. In the frontend set `NEXT_PUBLIC_DEMO_MODE=false`, `NEXT_PUBLIC_API_URL=http://localhost:8080/api`, then run `npm ci` and `npm run dev`. In the backend run `go run ./cmd/server`.

Password authentication, invite-only signup, MFA with a configured encryption key, PostgreSQL persistence and in-app notifications work locally. Invitation/reset emails go through the durable queue and save **private `.eml` files** in `backend/.local/mail` (or `PREVIEW_EMAIL_DIR`). Open the newest file locally and follow its link; no SMTP delivery occurs. Settings labels completed preview jobs “Preview saved.” Never serve or commit these files: they contain authentication tokens. Set `MAIL_DELIVERY=disabled` to turn previews off.

Google SSO/Drive, S3 and malware scanning remain explicitly unconfigured. Development files use private authenticated local storage with scanning reported disabled. The separate browser demo remains an explicitly labeled synthetic preview (`npm run demo`), with sample employees and payslips. Neither mode claims a real provider integration. Project environment health remains manually reported.

## Activate hosting and providers later

Keep both repositories and processes separate. Deploy the Next.js build alongside the backend service, or set `FRONTEND_UPSTREAM_URL` to a separate reachable frontend origin without a path. Build the frontend with `NEXT_PUBLIC_DEMO_MODE=false` and `NEXT_PUBLIC_API_URL=/api` for the shared public origin. A frontend-only request needs its own Next process; the Go binary does not embed Next.js.

Choose a real HTTPS hostname, then set `APP_PUBLIC_URL=https://YOUR_HOST`, `API_PUBLIC_URL=https://YOUR_HOST/api`, exact `ALLOWED_ORIGINS`, and `COOKIE_SECURE=true`. Terminate HTTPS at a reverse proxy forwarding all app traffic to Go. Native service and nginx examples live in this repository's `deploy/` directory. They use `/opt/northstar` paths as install examples; adapt them to your server. Never leave example hostnames in a real release.

Configure the Workspace domain in company settings after acquiring it. Install OAuth credentials and register `${API_PUBLIC_URL}/auth/sso/callback`. Add real SMTP credentials and switch `MAIL_DELIVERY=smtp`; configure a dedicated private Drive folder/service account, private S3 bucket and ClamAV endpoint. Complete the checks in [production operations](PRODUCTION.md), including payroll reconciliation and backup/restore. `APP_ENV=production` enforces all required providers and HTTPS and rejects preview email; deferred configuration is a local development state, not an activated production release.

The backend cannot host a live service on GitHub's source repository URL. Upload each repository independently using its README, then choose native hosting and supply the resulting app hostname when available. Secrets belong in deployment environment files, not GitHub source or chat.

## Gmail/email invitations and current roles

Sign in as Owner, then open **Workspace settings → Access & people**. Enter the recipient's name and Gmail/other email, choose Employee or Owner and create an invitation. Employee is the normal staff role; Owner grants payroll, benefits, onboarding/holiday management and request approvals. Existing HR accounts now use Employee permissions. The recipient sets a password through the single-use seven-day signup link, then signs in. A Workspace domain is only needed for Workspace SSO, not invited password accounts.

The native setup still has `MAIL_DELIVERY=preview`: invitations are saved as private local `.eml` previews and no real email is sent. For delivery, set `MAIL_DELIVERY=smtp`, `SMTP_ADDRESS`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` and the provider's `SMTP_IMPLICIT_TLS` setting in private backend environment configuration; restart the backend. `APP_PUBLIC_URL` must be reachable by the recipient; a localhost link works only on the host computer. Check Company & delivery for queued/sent/failed jobs. Disabled email mode creates a copyable link only. Never publish invitation links or local previews.
