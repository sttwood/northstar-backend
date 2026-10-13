# Architecture

## Repository boundaries

The root is a small workspace repository for a Node launcher and cross-repository documentation. Frontend and backend are nested **independent repositories**, excluded by the root `.gitignore`. Each can be cloned, built, tested, and deployed without the parent checkout.

Next.js renders the application shell. Interactive modules are client components. Ant Design supplies complex inputs and theme integration; local shadcn-style Button and Radix Dialog primitives handle actions and dialogs. Tailwind v4 and shared CSS tokens define a consistent mobile layout. React Flow is dynamically loaded for dependency maps; dnd-kit provides tasks and template sorting. TanStack Query owns API snapshots and invalidation. Zustand owns navigation and demo selection. Yup validates common request identity/title data, with domain-specific checks in forms and authoritative checks in Go.

## Data path

```text
Browser → Next.js UI → /api (credentialed HTTP) → Go HTTP handlers
                                                  ↓
                                      domain authorization / validation
                                                  ↓
                                      PostgreSQL transaction repository
                                                  ↓
                              employees / records / sessions / files / audit_log
```

The Go backend uses `net/http`, pgx, and bcrypt. Public JSON excludes password hashes. Opaque random sessions are stored as SHA-256 hashes in PostgreSQL and expire after 12 hours. Session cookies are HttpOnly, SameSite=Lax, and Secure under HTTPS. Password changes invalidate the employee's sessions. CORS and origin checks use exact configured origins. No frontend role grants server privileges.

## Persistence

- `companies`: tenant policies, workspace slug, Workspace domain and private Drive folder.
- `employees`: company-scoped unique email, reporting manager, lifecycle state, bcrypt hash and profile JSONB.
- `records`: indexed kind/employee ownership plus JSONB for each feature's domain data.
- `files`: tenant/employee ownership, scan verdict and metadata; private S3 bytes in production, filesystem in local development.
- `operations`: durable encrypted mail, Drive and deletion jobs, notifications, hashed invitations/challenges, payroll opening balances and legal holds.
- `account_security`: encrypted TOTP secrets, replay counter, hashed recovery codes and linked Google subject.
- `sessions`: hashed tokens and expiration.
- `audit_log`: immutable application action entries.
- `schema_migrations`: applied schema version.

Repository reads use a repeatable-read snapshot and authenticated tenant context. Writes take a per-company PostgreSQL advisory transaction lock, validate the company aggregate and persist changed rows atomically. This preserves leave balances, staged decisions and allocation consistency while allowing different companies to write concurrently. The v2 API uses indexed keyset SQL paging before redaction; personal statistics aggregate all matching records in SQL. Company writes still load a full tenant aggregate, so high-throughput large-company writes require further repository specialization and load testing.

Versioned migrations 001/002 are embedded in Go and applied transactionally. Future structural migrations should be versioned and applied explicitly; do not edit applied SQL to perform destructive migrations. No SQLite or Docker dependency is used.

## Authorization

| Resource                                  | Employee                                   | Owner / HR                          |
| ----------------------------------------- | ------------------------------------------ | ----------------------------------- |
| Own requests                              | Create, read, edit/cancel pending requests | Review and decide others' requests  |
| Decided leave/OT/document/general request | Read                                       | Read; decisions retained immutably  |
| Team approved leave                       | Read name/type/dates only                  | Full details                        |
| Profile                                   | Own details; edit own name/phone/location  | Edit employment details             |
| Salary / payslips                         | Own only                                   | Authorized access to all employees  |
| Projects / tasks                          | Assigned projects; manage team tasks       | Manage all projects and assignments |
| Discussion posts                          | Read assigned project; edit own posts      | Manage project discussion           |
| Holidays / benefits                       | Read                                       | CRUD                                |
| Onboarding                                | Own completion                             | Assign and manage                   |
| Roles                                     | No changes                                 | Owner only                          |
| Audit log                                 | No access                                  | Read                                |

Employees assigned to the current reporting-line approval stage can review that request, but cannot approve their own requests, access unrelated private files, upload official payroll documents, edit salary/allowance/team, or grant themselves roles. Owners cannot remove their own owner role. Supporting attachments must belong to a request for the selected employee. Files are fetched through authenticated endpoints; upload paths are random IDs rather than original filenames.

## Policy assumptions

Leave supports full days, half-days and 30-minute hourly increments inside company working windows, excluding weekends, recurring/observed team holidays and the centered unpaid break. Pending requests reserve fractional allowance. Approved historic duration is retained. Ordered manager/manager2/HR/owner stages snapshot eligible reviewers and record every decision.

Overtime uses same-day intervals and allows at most 16 hours per request. Overnight work uses separate requests. Thailand 2026 statutory payroll uses server salary/approved OT, certified allowances and verified YTD opening balances; finalized ledger entries are immutable. Payroll scope and provider setup are detailed in PRODUCTION.md.

## Demo isolation

The frontend browser demo explicitly uses localStorage and public fixtures. It does not call or fall back from the API. The `-demo` Go flag uses an ephemeral memory repository, sharing domain validation/authorization with PostgreSQL mode. Real production builds must set `NEXT_PUBLIC_DEMO_MODE=false`; real server startup omits `-demo` and `SEED_DEMO=true`.

## Production integrations

Google Workspace OIDC uses verified signatures, audience, nonce, PKCE, Workspace domain and invitation matching. Opaque sessions enforce active employees and tenant context; TOTP and recovery challenges persist hashed one-use state. SMTP, Drive and file deletion use durable jobs with retry leases. Files are scanned before private storage. Offboarding revokes access, transfers work and starts configurable retention; legal holds pause purge. Project environment health remains manually reported. See [the production guide](PRODUCTION.md) for configuration, statutory assumptions, backup deletion limits and live verification.
