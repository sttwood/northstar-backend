# API contract

Base URL: `http://localhost:8080/api` locally; `/api` behind the production reverse proxy. JSON errors use `{ "error": "Human-readable message" }`. Typical codes: 400 validation, 401 sign-in required, 403 permission denied, 404 missing, 409 duplicate email, 429 sign-in throttle, 500 internal error. Private responses use `Cache-Control: no-store`.

## Authentication

| Method | Path             | Body / behavior                                                          |
| ------ | ---------------- | ------------------------------------------------------------------------ |
| POST   | `/auth/login`    | `{email,password}`; sets 12-hour HttpOnly session cookie                 |
| POST   | `/auth/logout`   | Invalidates current session and clears cookie                            |
| POST   | `/auth/password` | `{current,password}`; 12–72 byte password; invalidates employee sessions |
| GET    | `/bootstrap`     | `{me,employees,records,files,audit}` with per-user redaction             |

Browser requests use `credentials: include`. Exact frontend origins are configured in `ALLOWED_ORIGINS`. Cookies use SameSite=Lax and the `/api` path; enable Secure for HTTPS. Authorization bearer tokens are accepted for non-browser API clients if supplied with an existing opaque session token; login returns only the cookie and safe employee fields.

Example with a cookie jar:

```sh
curl -c cookies.txt -H "Content-Type: application/json" -d '{"email":"maya@northstar.test","password":"NorthstarDemo2026!"}' http://localhost:8080/api/auth/login
curl -b cookies.txt http://localhost:8080/api/bootstrap
```

The sample password works only when explicitly seeded or running `-demo`. Protect and remove any local cookie jar after testing.

## Records

```json
{
  "id": "server-generated",
  "kind": "leave",
  "employeeId": "maya",
  "title": "Vacation",
  "status": "pending",
  "createdAt": "server-generated UTC RFC3339",
  "data": {
    "type": "vacation",
    "startDate": "2026-11-09",
    "endDate": "2026-11-10",
    "reason": "Family time"
  }
}
```

| Method | Path            | Behavior                                                                                                                                    |
| ------ | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| POST   | `/records`      | Creates record; server supplies ID/time, validates kind/status/ownership/data                                                               |
| PATCH  | `/records/{id}` | Partial `{title?,status?,data?,employeeId?}`; `data` replaces the full data object; employee reassignment is allowed for project tasks only |
| DELETE | `/records/{id}` | Removes an authorized editable record; project removal also removes its tasks/discussion; decided requests are retained                     |

| Kind       | Statuses                  | Key data                                                                                |
| ---------- | ------------------------- | --------------------------------------------------------------------------------------- |
| leave      | pending/approved/rejected | type vacation/sick/personal, startDate, endDate, reason; server computes days           |
| overtime   | pending/approved/rejected | date, startTime, endTime, projectId, reason; server computes hours                      |
| document   | pending/approved/rejected | documentType, language, purpose                                                         |
| request    | pending/approved/rejected | category, description                                                                   |
| ticket     | open/in-progress/done     | category, priority low/normal/high/urgent, description                                  |
| project    | active/archived           | startDate, dueDate, members[], allocations{}, milestones[], guide, repository, layout{} |
| task       | open/in-progress/done     | projectId, dueDate, priority, description, dependsOn[]; employeeId is assignee          |
| comment    | active                    | projectId; title is the short discussion message                                        |
| holiday    | active                    | date, team (or All teams), description                                                  |
| benefit    | active/archived           | category, description, allowance, details, contact                                      |
| onboarding | open/done                 | description, dueDate; employeeId is the assigned person                                 |
| template   | active                    | company, footer, fields[]                                                               |

Dates use `YYYY-MM-DD`; times use 24-hour `HH:mm`. New leave/OT/document/general requests always start pending. Owner review changes status; rejected requests need `data.decisionNote`. The server records `decidedBy`/`decidedAt`. Existing decided requests cannot be changed or deleted. Leave reasons are redacted for teammates. Employees can complete their onboarding but cannot rewrite assigned instructions.

## Project environments

Project `data.environments` is an optional object keyed only by `dev`, `sit`, `uat`, and `prod`. Each environment has this shape:

```json
{
  "url": "https://sit.example.com",
  "apiUrl": "https://sit.example.com/api",
  "branch": "release/1.2",
  "version": "1.2.0",
  "status": "healthy",
  "notes": "Manual environment notes",
  "docs": [
    {
      "id": "setup",
      "title": "Setup guide",
      "url": "https://example.com/docs",
      "description": "Developer setup instructions"
    }
  ],
  "timeline": [
    {
      "id": "release-12",
      "title": "Release 1.2",
      "date": "2026-10-13",
      "description": "Planned deployment",
      "status": "planned"
    }
  ],
  "accounts": [
    {
      "id": "qa-account",
      "name": "QA account",
      "username": "qa@example.com",
      "role": "Tester",
      "url": "https://sit.example.com/login",
      "secretRef": "https://vault.example.com/items/sit-qa",
      "status": "active"
    }
  ]
}
```

Managers edit environments through `PATCH /records/{projectId}`; assigned employees can read them. Send the complete project `data` object when saving, preserving its other fields. Environment status is `healthy`, `maintenance`, or `offline`; timeline status is `planned` or `complete`; account status is `active` or `disabled`. Health is manually recorded, not polled.

URLs must use HTTP(S) without embedded credentials, including the optional password-manager link in `secretRef`. Timeline dates must be valid `YYYY-MM-DD` dates. Each list allows up to 100 entries, and entry IDs must be unique within the environment. Unknown environment fields, including password/token fields, are rejected. Account entries do not provision external users.

## Employees

- `POST /employees`: owner only; `{name,email,password,role,team,title,salary,startDate,leaveAllowance}`. Password 12–72 bytes, unique email. Only owners may create employees or assign roles. Legacy HR accounts have employee permissions.
- `PATCH /employees/{id}`: `{name?,phone?,location?,team?,title?,salary?,role?,leaveAllowance?}`. Employees may edit only their own name/phone/location. Managers maintain employment fields. Owner role changes require an owner; an owner cannot demote themself.

## Files

- `POST /files`: multipart form `file`, `employeeId`, `category` (`payslip`/`document`/`attachment`), `month`, `year`, optional `recordId`. Maximum 10 MB. Payslips require month/year and owner permission; official documents require owner permission; personal supporting attachments must belong to the chosen employee/request. Returns safe metadata.
- `GET /files/{id}`: authenticated attachment download; only the employee concerned and owners.
- `DELETE /files/{id}`: owners may delete; employees may remove their own supporting attachments, but not official documents/payslips.

Stored bytes are outside the frontend and never exposed as a public directory. Filenames are sanitized for download; disk paths use server-generated IDs. PDF generation happens in the authorized frontend, then uses the same upload endpoint. File storage and PostgreSQL are backed up together.

## Health

`GET /healthz` and `GET /readyz` are outside `/api` and do not require a session. Readiness returns 503 when PostgreSQL is unavailable.

## Production API additions

All private routes resolve company from the authenticated session, never a client-provided tenant/role. All users remain limited to that company. Company workspace slug is needed when the same email exists in multiple companies.

| Method/path                             | Request / result                                                                                                                                                                        |
| --------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| GET /auth/providers                     | Public configured Google/MFA flags; signup is invitation-only                                                                                                                           |
| POST /auth/login                        | {email,password,company}; returns user/cookie or {mfaRequired,challenge,setupRequired} without a session                                                                                |
| POST /auth/signup                       | {token,name,password}; consumes invitation, returns 201; sign in separately                                                                                                             |
| POST /auth/mfa/challenge                | {challenge,code}; one-time TOTP/recovery verification. For required enrollment: {challenge,enroll:true} returns setup key, then {challenge,code} returns session/recovery codes         |
| GET /auth/sso/start?company=slug        | Redirect to Google with PKCE, nonce and cookie-bound state                                                                                                                              |
| GET /auth/sso/callback                  | Google callback; active/invited Workspace identities only; redirects to app or MFA                                                                                                      |
| POST /auth/password-reset/request       | {email,company}; generic 204 for unknown users; requires SMTP                                                                                                                           |
| POST /auth/password-reset/confirm       | {token,password}; single-use reset and session revocation                                                                                                                               |
| GET /security/mfa                       | Safe enrollment state and remaining recovery count                                                                                                                                      |
| POST /security/mfa/enroll               | {password}; setup key/URI, never persist in browser storage                                                                                                                             |
| POST /security/mfa/confirm              | {code}; enables MFA, returns ten one-time codes and revokes sessions                                                                                                                    |
| POST /security/mfa/disable              | {password,code}; blocked by required-MFA company policy                                                                                                                                 |
| POST /invitations                       | Owner: {email,role,name?,team?,title?,managerId?}; returns expiring inviteUrl and emailQueued                                                                                           |
| GET /company                            | Current policy, configured integration flags, owner queue/invitation metadata (latest 100)                                                                                              |
| PATCH /company                          | Owner: full editable policy fields; name/domain/requireMfa/approvalStages/workdayHours/workdayStart/workdayEnd/retentionDays/auditRetentionDays/notificationRetentionDays/driveFolderId |
| POST /companies                         | Owner: {name,slug,domain?}; isolated company and copied owner identity                                                                                                                  |
| POST /employees/{id}/offboard           | Owner: {transferTo?,reason?,legalHold?}; revokes access and transfers work                                                                                                              |
| PATCH /employees/{id}/retention         | Owner: {legalHold}; offboarded employee only                                                                                                                                            |
| POST /retention/run                     | Owner-triggered retention pass                                                                                                                                                          |
| GET /notifications                      | Current user's latest 100 notifications                                                                                                                                                 |
| PATCH /notifications/{id}               | Mark own notification read                                                                                                                                                              |
| POST /integrations/drive/sync           | Owner: queue managed exports and tagged paginated imports                                                                                                                               |
| POST /operations/{id}/retry             | Owner: requeue a failed email/Drive/deletion job; tenant-scoped                                                                                                                         |
| GET /v2/bootstrap                       | Bounded initial workspace, company, full-data personal statistics and collection cursors                                                                                                |
| GET /v2/{records,employees,files,audit} | {items,nextCursor,limit}; query limit=1..200, cursor, kind, status, employeeId, projectId, search; records additionally approvals=true                                                  |
| GET /statistics?year=2026&month=10      | Full-data personal vacation/sick/OT/pending/open totals                                                                                                                                 |
| POST /payroll/opening                   | Owner: {employeeId,year,throughMonth,gross,withheld,sso,provident}; verified YTD totals, no overlap with finalized months                                                               |
| POST /payroll/calculate                 | Owner: employeeId/year/month and certified adjustments; server salary, approved OT and YTD; returns input/result                                                                        |
| POST /payroll/finalize                  | Same input; atomic immutable payroll ledger; duplicate employee/period rejected                                                                                                         |

Payroll adjustments: bonus, taxableBenefits, reimbursements, otherDeductions, insured, optional socialSecurityWage, providentEmployeeRate/providentEmployerRate (decimal fractions), spouseNoIncome, children30, children60, parents, certifiedOtherAllowance. Only TH/2026 ordinary resident employment is supported. Opening balances and special eligibility require payroll review. The separate PDF upload links recordId to the finalized ledger.

Leave data adds unit=day|half-day|hour, period=morning|afternoon for half-day, startTime/endTime for hourly. Server computes fractional days, overlap and approvalSteps/currentApproverIds; clients cannot replace approval metadata. Holiday data adds recurrence=annual|none and observed=next-weekday|none. Employee employment updates accept managerId with cycle checks. File uploads are scanned before private storage; production rejects unavailable scanning. Deletes remove metadata and queue provider deletion rather than claiming immediate remote erasure.

Use v2 for large queries. Legacy /bootstrap remains unbounded. Loaded list filters operate on loaded pages; use server query filters or Load more for older data. Production setup, tax sources, retention/backup limits and operational checks belong to the backend's PRODUCTION guide.

`GET /company` exposes `integrations.emailDelivery` as `disabled`, `preview`, or `smtp`. `integrations.smtp` is false during local preview. Email job `delivery` records the adapter used on successful processing, so historical preview jobs remain distinguishable after enabling SMTP. Preview messages are private backend files and have no HTTP download endpoint. Optional `FRONTEND_UPSTREAM_URL` proxies non-API app pages only; unknown `/api/*` paths still return backend 404 responses.

## Project hierarchy and resource files

Project `data.parentId` is an optional project ID; absent or empty means top-level. Same-company existence, acyclic ancestry and active-parent rules are validated atomically. Parent membership never grants child access. Archiving a parent requires archiving/reassigning active children; deletion requires moving/deleting all children and deleting linked files first. Migration 003 adds hierarchy and file-query indexes automatically at startup.

`POST /files` multipart now supports `category=project-resource`, `projectId`, `resourceKind=image|api-spec|data-dictionary|document|file`, optional `description` (2,000 characters) and `file`. The server sets the uploader to the authenticated employee and requires project access plus an active project. `recordId` cannot be supplied for resources. Maximum 10 MB; PDF/PNG/JPEG/WebP/TXT/MD/CSV/JSON/YAML/XLSX/DOCX only. JSON and Office structure are checked; macro archives and expanded Office contents above 50 MB are rejected. Production scanning/storage requirements are unchanged. Responses include `projectId`, `resourceKind`, `description` and validated `mimeType`.

`GET /v2/files?kind=project-resource&projectId=ID&search=term&limit=50&cursor=ID` pages resources with membership and company filtering. Authenticated `GET /files/{id}` and `DELETE /files/{id}` use project permissions; deleting requires owner or current-member uploader. Uploader ownership cannot bypass revoked membership. Retention preserves shared resources after uploader offboarding; owners can delete them explicitly.

## Owner payslip configuration

Template records use normal record CRUD, with create/edit/delete restricted to owner. Only owners read/use templates for payroll generation. `data` accepts `layout=classic|table`, company/taxId/address/payslipTitle/footer/accentColor, unique `fields` (maximum 60, each 1–80 characters), `fieldLabels` (field key → printed label, maximum 160 characters), and `infoFields`, `earningFields`, `deductionFields`, `ytdFields`. Sections must contain existing fields once; table layout requires earnings and `Net pay`, with every other field assigned. `Net pay` cannot be assigned to a section. Company/tax ID maximum 200 characters, title 120, address 1,000, footer 2,000; accent is a six-digit hex color. Legacy templates without layout/sections remain valid.

Owner-only reference upload: `POST /files` with `category=payslip-reference`, `recordId=TEMPLATE_ID`, and a PDF file. Owner identity is enforced; files cannot carry project metadata. `GET /v2/files?kind=payslip-reference&recordId=TEMPLATE_ID` lists references. Non-owners cannot list/read/download/delete them, including when the stored employee ID matches. References never enter Drive export. Delete attached references before deleting a template; existing generated payslips are retained. Configuration is presentation only and does not OCR/import reference amounts or alter statutory calculations.

Current authorization policy: only owners have administration capabilities (including invitations, payslips, benefits, onboarding assignment, holidays and request decisions). Legacy HR accounts use employee permissions. New requests snapshot one owner stage; existing pending reporting-line requests can be finalized by an owner, preserving earlier decisions and marking remaining pending stages superseded. Employees retain own requests, private payslips, onboarding completion and assigned project work. Approval inbox pages are empty for non-owners. Company approvalStages accepts only ["owner"]. Invitation responses include deliveryMode (preview/smtp/disabled) alongside emailQueued; preview means private local capture, not actual email delivery.

Environment Docs uploads use category=project-resource plus environment=dev|sit|uat|prod, projectId and resourceKind; only owners upload/delete these files, assigned project members download. GET /v2/files accepts environment alongside existing project/record filters. Non-resource files reject environment metadata. Existing project data.environments.docs URL entries remain readable for compatibility; new UI documentation uses uploaded files.

## Profile photos and community

POST /files accepts category=avatar, file, employeeId (forced to the authenticated user). PNG/JPEG only, maximum 2 MB, source dimensions at most 4096 per side / 16 megapixels. Scanning is still required in production. JPEG orientation is applied and metadata stripped while resizing to 512 px. The transaction replaces the employee avatarFileId and queues old blob deletion. GET /files/{id} serves the current photo inline to same-company authenticated colleagues. DELETE clears the profile reference; only self or owner can delete. Project/request metadata is forbidden for avatars. Other private file permissions are unchanged.

POST /invitations no longer needs name; the UI collects email/role and employees enter their full name at signup. The optional API name remains compatible with older clients.

| Method/path                        | Behavior                                                                                                                                            |
| ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| GET /community                     | Paginated company feed; type=help/knowledge, status=open/resolved, search, cursor, limit (default 20, maximum 50); returns items/authors/nextCursor |
| POST /community/posts              | {title,body,type,tags}; server assigns employee/company/creation time                                                                               |
| GET /community/posts/{id}          | {post,solution,authors}; pins accepted solution independently of reply pages                                                                        |
| PATCH /community/posts/{id}        | Own unresolved post: {title,body,tags,type?}; type is immutable                                                                                     |
| GET /community/posts/{id}/answers  | Paginated replies/authors with cursor/limit                                                                                                         |
| POST /community/posts/{id}/answers | {body}; active member, unresolved question or knowledge discussion                                                                                  |
| PATCH /community/answers/{id}      | {body}; own unaccepted answer only                                                                                                                  |
| POST /community/posts/{id}/resolve | {answerId}; question author or owner, different helper, one immutable solution; duplicate acceptance 409                                            |
| GET /community/summary             | Own full-data points/counts/streak/16 achievements; top ten company helpers with public author fields                                               |

Titles: 1?200 characters; body: 1?12000; tags: maximum five unique labels of 1?32 characters. Legacy bodies render as plain text; optional rich documents use a validated schema and safe React elements. Each accepted help answer earns 10 points, computed from immutable accepted references rather than client counters. Self-awards and cross-question answers are rejected. Knowledge posts do not award solution points. Posting/answering records one contribution day in Asia/Bangkok; reads, edits and acceptance do not. Current streak and best-streak badge progress are computed on the server. Generic record CRUD/list routes reject/exclude community records, preventing bypass of accepted-content rules. Migration 004 adds company/author/page/activity indexes. Community reads are paginated and author projections omit private employee fields.

## Community rich text and public badges

GET /api/community/badges?employeeIds=id1,id2 returns a map of same-company employee IDs to earned achievements only (up to 100 IDs per request). Unknown/cross-company IDs are omitted; known users without earned badges return []. Requires authentication. Uses three batched SQL aggregate queries rather than per-author summary requests. Public badges expose no payroll or personal contact data.

Post/answer creation and editing accept optional bodyDoc alongside body. bodyDoc is a Tiptap JSON document; the server derives the searchable plain body from it. Legacy body-only requests remain supported, and body-only edits remove stale formatting. Example document:

    { "type": "doc", "content": [{ "type": "paragraph", "content": [{ "type": "text", "text": "Hello", "marks": [{ "type": "bold" }] }] }] }

Allowed nodes: doc, paragraph, headings (level 2/3), bulletList, orderedList (start 1-10000), listItem, blockquote, codeBlock, horizontalRule, hardBreak, text, image (uploaded community file IDs only). Allowed text marks: bold, italic, underline, strike, code, link. Links require credential-free HTTP(S) URLs (max 2048 characters); target/rel/class are normalized. HTML, external image URLs, unknown fields/attributes and unsafe links are rejected with 400. Documents are limited to 256 KiB, 2500 nodes, depth 16 and 12000 plain-text characters. Existing ownership, immutable accepted-content rules, tenant isolation and contribution-day rules apply unchanged. Documents use existing record JSONB; no database migration is needed.

## Community images

Upload PNG/JPEG through POST /api/files with category=community-image. Identity is forced to the authenticated uploader. No project/request metadata is accepted. Sources are limited to 2 MiB, 4096 pixels per side and 16 megapixels; scanned before storage, oriented/resized to 1600 pixels and re-encoded without metadata. Production scanning remains fail-closed. Downloads require authentication and use inline image content types.

Rich documents accept image nodes with attrs={fileId,alt}; URLs, base64 and arbitrary HTML attributes are rejected. Up to 20 images per document; alt text is limited to 300 characters. The server verifies same-company community file references transactionally. Unpublished uploads are private to uploader/owner and cannot be attached by another author. Publishing binds the file to company knowledge history, enabling colleagues to view it. Published assets cannot be deleted through ordinary file deletion, including from accepted answers. Community images are excluded from generic personal-drive paging and payroll Drive synchronization. Unpublished drafts older than 24 hours are queued for deletion during daily retention; published learning images survive uploader offboarding alongside shared learning records.

## Points shop

| Method and path                | Behavior                                                                                                                                                  |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| GET /api/shop                  | Paginated active catalog; search, cursor, limit (max 50). Owner all=true includes hidden items.                                                           |
| POST /api/shop/items           | Owner only: title, description, kind, effect, price, stock, active.                                                                                       |
| PATCH /api/shop/items/{id}     | Owner edits title/description/price/remaining stock/visibility. Kind and effect are immutable. Set active=false to archive without losing purchases.      |
| GET /api/shop/wallet           | Own lifetime earned, spent, balance and bounded ownedDecorations list.                                                                                    |
| POST /api/shop/purchases       | {itemId,key}; atomic redemption with a 16-100 character retry key. Reusing the same key/item returns the same purchase (200); a new purchase returns 201. |
| GET /api/shop/purchases        | Own paginated purchase history; owner queue=true returns company pending fulfillment.                                                                     |
| GET /api/shop/purchases/{id}   | Own purchase, or same-company owner access for fulfillment; foreign/missing returns 404.                                                                  |
| PATCH /api/shop/purchases/{id} | Owner fulfills a pending physical reward once with private delivery details/code (1-2000 characters).                                                     |
| POST /api/shop/equip           | {purchaseId,slot}; equips an owned decoration. Empty purchaseId resets the selected slot.                                                                 |

Reward kinds: coupon, voucher, discount, border, name-color, name-style. Price is an integer 1-1000000. Stock is remaining units, -1 for unlimited, or 0 for unavailable. Border presets: blue/gold/rainbow/pulse/glow; name colors: blue/purple/green/rose/gold; name styles: bold/italic/glow/gradient. Presets avoid arbitrary CSS, script or external resources. Decorations are delivered immediately and cannot be purchased twice for the same slot/effect. Motion respects reduced-motion preferences.

Earned points derive from immutable accepted community solutions (10 per question). Pending and fulfilled purchases immediately count as spent. Transactional company locking prevents concurrent overspending and overselling. Price/description snapshots preserve purchase history through catalog edits. Coupon/voucher/discount delivery is manual by an owner, with terms/expiry supplied in description or delivery details; no merchant integration or automatic voucher-code generation is implied. Delivery codes appear only in buyer history and owner fulfillment access. Purchases/equipping do not award points or contribution days. Community achievement totals remain lifetime gross earnings. Generic record CRUD cannot modify shop records, prices, purchases or decorations. Offboarding clears equipped styles and personal delivery/retry metadata while preserving spending accounting.

Migration 005 adds catalog/purchase/notification indexes automatically on backend startup; records and operations remain PostgreSQL JSONB.

## Navbar search and notifications

Navbar search queries authorized /api/v2/records, /api/v2/employees, /api/v2/files and /api/community search pages across server data, independent of the bootstrap's first 200 rows. Results are bounded and the user can refine the term. Generic record paging additionally supports recordId for exact authorized target lookup. Search treats % and _ literally; masked team leave titles cannot be inferred by searching private titles.

GET /api/notifications accepts cursor/limit (default 20, max 50), returns items, nextCursor and the full unreadCount. Results are newest-first, employee- and company-scoped, with indexed keyset paging. PATCH /api/notifications/{id} marks one own item read; PATCH /api/notifications/all marks all own notifications read, including unloaded pages. Navbar polling is every 15 seconds. Request submissions/decisions, community replies/accepted solutions, pending shop fulfillment and delivered rewards open their authorized destinations. Notification payloads never contain private voucher codes. Existing SMTP request delivery remains separate and follows configured providers.

## Holiday JSON import and font rewards

POST /api/holidays/import/preview validates the monthly JSON and returns year, created, skipped and rows without writing. POST /api/holidays/import validates and imports atomically, repeating duplicate detection under the company write lock. Both endpoints require an active owner and reject unknown fields. Input is the calendar object directly:

    {"year":2026,"month":[{"id":1,"name_th":"January (Thai label)","name_en":"January","holidays":[{"date":1,"day":"th","name_th":"New year (Thai name)","name_en":"New Year's Day"}]}]}

Use Gregorian years 2000-2200, 1-12 unique month IDs, valid numeric month days and up to 500 holidays. At least one English/Thai name is required per holiday; each name is bounded to 200 characters. Month labels are optional metadata; numeric IDs determine dates. day is optional, with su/mo/tu/we/th/fr/sa or English weekday aliases (case-insensitive). A supplied weekday must match the actual date. The UI accepts pasted JSON or a JSON file up to 256 KiB and offers a downloadable valid EN/TH example.

Imported records are active, apply to All teams, and default to recurrence=none and observed=none. Owners can edit individual recurrence, observance and bilingual names through existing holiday CRUD. Same date/occurrence, all-team scope and a matching normalized English/Thai name make a duplicate, including staged rows and existing annual holidays. Duplicate imports skip existing rows without overwriting them. Distinct holidays on the same date are allowed. Any invalid row rolls back the whole import. Names persist as data.nameEn/data.nameTh and the interface selects the current locale with title fallback. Calendar, mini-calendar, leave forms and owner lists load all holiday pages through bounded v2 kind=holiday queries. No schema migration is required.

Name-style rewards additionally accept pixel, bubble and medieval, preserving bold/italic/glow/gradient for existing rewards. The three new styles use licensed fonts bundled with the frontend; Thai characters fall back to local Noto Sans Thai. Frontend inventory compares a purchase's slot/effect with the server profile to show Equip or Unequip. Unequip calls the existing POST /api/shop/equip with an empty purchaseId for that slot; ownership and spent points remain unchanged.

## Owner points and workspace status

GET /api/points/{employeeId} returns earned, adjusted, spent, balance and ownedDecorations. GET /api/points/{employeeId}/adjustments?cursor=&limit=20 returns items/nextCursor; employees read only their own, owners read company users. POST /api/points/{employeeId}/adjustments is active-owner-only and takes mode=add|subtract|set, integer amount 0-1000000 (positive unless set), nonblank reason up to 500 characters, key (16-100 characters), and expectedBalance. Set requires expectedBalance; the UI sends it for all actions. Stale balances/overdrafts return 409. A tenant-scoped retry key is idempotent for identical actor/employee/action/amount/reason and conflicts if reused for different inputs. Returns the immutable operation, including before/after/delta. Targets must be active same-company users. Adjustments do not modify contribution achievements or purchases, are spendable by the shop, and create a private notification. Migration 006 adds indexed keyset history. Reasons are visible to the target employee; retention purges adjustments with that employee.

GET /api/workspace/status returns mode, message, messageTh, optional expectedAt and updatedAt. PATCH is active-owner-only, validates operational|degraded|read-only|maintenance, bounds each notice to 500 characters, requires at least one notice outside operational, validates optional RFC3339 expectedAt and replaces updatedAt with server time. Company JSONB stores the settings and audit logs track changes. No new provider/env configuration.

Authenticated non-owner business access returns HTTP 503 with {error, workspaceStatus} and Retry-After: 30 in maintenance. Read-only blocks unsafe business methods while allowing GET/HEAD. Owners bypass this gate for recovery. Status polling, logout, password/MFA controls, public authentication and health probes remain accessible. Degraded is informational; expectedAt does not automatically restore service. Modes are company-scoped, read through a direct indexed company lookup on requests; already-running requests/background jobs may finish. Frontend polling is every 15 seconds and restricted responses refresh status immediately. Presentation previews /status/400, /status/500, /status/success etc. are frontend pages, separate from actual API HTTP errors and Next.js error/not-found/loading boundaries.

## v1.0 core production profile

Company service connections additionally report productionProfile and fileUploads. Explicit PRODUCTION_PROFILE=core permits deferred SMTP/Google/Drive but still requires real production HTTPS/secure cookies/encryption and forbids demo seeds/previews. Without both S3 and ClamAV, POST /api/files returns 503 before parsing/storing bytes and storage/fileUploads report disabled/false. Full remains the strict default. Fresh initialization creates one configurable Superadmin with owner permissions on the default company; credentials are first-boot-only, never an HTTP public account-creation shortcut.
