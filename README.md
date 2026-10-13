# Northstar backend

Independent Go HTTP API with PostgreSQL persistence, private file storage, bcrypt passwords, opaque session cookies, domain validation, and role-based access. Native deployment; no Docker.

For the configured Nexilar workspace and deferred provider setup, see [Nexilar setup](docs/NEXILAR_SETUP.md). `COMPANY_NAME=Nexilar` initializes a new company; existing company names are changed through Workspace settings. `FRONTEND_UPSTREAM_URL=http://127.0.0.1:3000` lets this backend serve the public app origin while Next.js remains a separate process/repository. `/api`, `/healthz`, and `/readyz` remain backend routes.

For local invitation/reset workflows, `MAIL_DELIVERY=preview` saves private `.eml` messages under `PREVIEW_EMAIL_DIR` (default `.local/mail` relative to the backend directory). Nothing is emailed. `MAIL_DELIVERY=smtp` requires SMTP configuration; `disabled` disables delivery. Production refuses preview delivery. Do not expose, sync or commit previews containing authentication links.

## Requirements

Go 1.27.2+, PostgreSQL 16+, and Git. A local temporary API demo can run without PostgreSQL. If using an older Go installation, set `GOTOOLCHAIN=auto` in your terminal to let Go fetch the version pinned in go.mod. The parent workspace launcher enables this for its own child processes without changing your system Go configuration.

```sh
go mod download
go run ./cmd/server -demo
```

Demo API: http://localhost:8080/api. Demo accounts: `maya@northstar.test` (employee), `emma@northstar.test` (owner), `mia@northstar.test` (HR). Password: `NorthstarDemo2026!`. Demo writes disappear when the process stops. Use this mode locally only.

## Persistent mode

Copy `.env.example` to `.env`. Create a database with `createdb -U postgres northstar`. Configure:

```dotenv
DATABASE_URL=postgres://postgres:YOUR_PASSWORD@localhost:5432/northstar?sslmode=disable
ADMIN_EMAIL=owner@yourcompany.com
ADMIN_PASSWORD=YOUR_UNIQUE_12_TO_72_BYTE_PASSWORD
ALLOWED_ORIGINS=http://localhost:3000
UPLOAD_DIR=./uploads
SEED_DEMO=false
COOKIE_SECURE=false
```

```sh
go run ./cmd/server
```

The idempotent embedded migrations create tables and apply versions 001/002. Initial owner credentials are used only for an empty database. They do not reset an existing account. Sign in with company workspace `northstar`. `SEED_DEMO=true` seeds the sample workspace only in an empty database; never use it for real deployments. Real mode fails explicitly if PostgreSQL is unavailable.

The [production guide](docs/PRODUCTION.md) covers Google Workspace SSO, MFA, SMTP, Google Drive Shared Drives, private S3/ClamAV files, Thailand 2026 payroll, approval stages, tenancy, offboarding/legal holds and operational limitations. Set `APP_ENV=production` to enforce provider/HTTPS configuration. External credentials are supplied by your deployment and are never seeded.

Seeded workspaces include Dev/SIT/UAT/Prod project details and 24 real synthetic payslip PDFs: July–September 2026 for all eight sample employees. The embedded sample bytes are installed into `UPLOAD_DIR`; downloads still require an authorized session. No real payroll is placed in public assets.

To add the new samples to an **existing demo database**, set `SEED_DEMO=true` in its `.env` and run:

```sh
go run ./cmd/server -seed-fixtures
```

This command exits after adding missing sample files and environment details. It preserves employee edits, custom records, existing files, and configured environments. It verifies the original eight demo account IDs/emails and refuses a different workspace. Production with `SEED_DEMO=false` never installs sample files.

Project environment updates inherit project permissions. Only managers can maintain settings, documents, release events, and account references. Assigned employees can read them. URLs must be HTTP(S) without embedded credentials. Password/token fields are rejected; account entries link to a team password manager.

Formatting: `gofmt -w cmd internal`. SQL and documentation follow the checked-in layout. From the parent workspace, `npm run format` handles every repository.

## Checks

```sh
go test ./...
go vet ./...
```

Set `TEST_DATABASE_URL` to run the actual PostgreSQL persistence/rollback integration test. It creates and removes a uniquely named test schema. Use a dedicated test database and a role with schema creation permission. Domain tests cover working days, reserved balance, overlaps, cycles, allocations, and self-approval; HTTP tests cover privacy, sessions, CORS, approved-record immutability, and file isolation.

## Build and deploy

```sh
go build -trimpath -o bin/northstar ./cmd/server
```

On Windows use `bin/northstar.exe`. Deploy the binary as a native service with `DATABASE_URL`, exact `ALLOWED_ORIGINS`, `COOKIE_SECURE=true`, and a **persistent** `UPLOAD_DIR`. Use PostgreSQL TLS (`sslmode=verify-full` and a trusted CA where supported), HTTPS, and a reverse proxy. Run without `-demo`. Back up both PostgreSQL and uploads together. The frontend should use a same-origin `/api` path for cookie authentication.

Endpoints: `/healthz` (liveness), `/readyz` (database readiness), and `/api/*`. Port defaults to 8080 and can be set through `PORT`. The process handles Ctrl+C/SIGTERM and allows up to 10 seconds for shutdown. Maximum file size is 10 MB. Payslips and official documents must contain a PDF header; attachments accept PDF, PNG, JPEG, and plain text.

See [API contract](docs/API.md) and [architecture](docs/ARCHITECTURE.md). Indexed keyset queries bound large reads. Aggregate writes serialize within each company; load-test your intended size and specialize domain repositories for high write throughput.

## GitHub

This directory is its own repository. Create an empty GitHub repository, then `git add .`, `git commit -m "Initial Northstar backend"`, `git remote add origin YOUR_REPO_URL`, and `git push -u origin main`. Runtime environment files, uploaded files, binaries, and coverage output are ignored.
Parent/child projects, private project resources and owner payslip reference/template configuration are available through the existing record/file APIs. Migration 003 installs automatically. See [API additions](docs/API.md#project-hierarchy-and-resource-files) for hierarchy, file formats, permissions and template payloads. Reference attachments are owner-only and do not import payroll amounts. Project resources remain shared when an uploader is offboarded.

Administration and approvals are now owner-only. Legacy hr accounts use employee permissions; normal invitations offer Employee or Owner. Gmail/password signup is supported through invitations without a Workspace domain. Delivery follows MAIL_DELIVERY: preview saves a local private email, smtp sends through configured SMTP, disabled creates a copyable invitation link only. Environment Docs use private per-environment uploaded resources.
Community uses authenticated company-scoped `/community` APIs, paginated posts/replies and public author projections. Accepted solutions award 10 server-computed points with self/duplicate guards; milestones and Bangkok contribution streaks unlock 16 badges. Migration 004 installs its indexes automatically. Profile photo uploads use the private file/scanning pipeline, normalize PNG/JPEG and replace `avatarFileId`; invitation `name` is optional and employees supply their actual name at signup. See [the API additions](docs/API.md#profile-photos-and-community).

Migration 005 installs shop/purchase/notification indexes. Dedicated shop APIs enforce immutable point spending, owner catalog/fulfillment permissions and private delivery details. Community image files are scanned/private drafts until published and follow shared learning retention. See [API contracts](docs/API.md#points-shop). No external merchant provider is required for manual reward fulfillment.

Owner-only POST /api/holidays/import/preview and POST /api/holidays/import validate monthly calendars and import duplicate-safely under the company lock. Holiday records retain bilingual names in existing JSONB; no new migration is needed. Name-style rewards also accept pixel/bubble/medieval. See the independent API contract for payloads and permissions.

Owner-only point adjustments and company app status use dedicated authenticated endpoints; see docs/API.md. Additive migration 006 adds an index for points adjustment history on startup. Owners retain access during maintenance; employee business access is enforced by the API.

For v1.0 deployment, follow [DEPLOY_V1.md](docs/DEPLOY_V1.md). The root render.yaml provisions native Go and managed PostgreSQL. Explicit PRODUCTION_PROFILE=core supports deferred providers while keeping uploads disabled without S3 plus ClamAV; full remains strict. Fresh installs create one Superadmin from private environment values, without demo data.

Already deployed on Vercel/Render Free? See [FREE_SERVICES.md](docs/FREE_SERVICES.md) for private R2, opt-in `UPLOAD_POLICY=images-only` profile/community images, SMTP alternate ports, Google deferral and database expiry planning. Default scanned policy and all general-file uploads still require ClamAV. The image-only option is not antivirus scanning.
