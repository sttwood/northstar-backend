# Connect the deployed app using free allowances

Deployment: `https://northstar-intranet.vercel.app`. The Go API and PostgreSQL are already on Render. Google Workspace and Drive are deferred because you do not have a Workspace domain. No Docker is needed. This guide does not create accounts, change your live configuration or move existing data.

## 1. Enable profile photos with a private R2 bucket

The upload error is an intentional backend restriction, not a Vercel image setting. The default `UPLOAD_POLICY=scanned` requires private object storage **and** ClamAV for every upload. Render Free has an ephemeral filesystem and cannot attach a persistent disk. Do not use its upload directory for production files. [Render Free limitations](https://render.com/docs/free).

This update adds an explicit `UPLOAD_POLICY=images-only` option for `PRODUCTION_PROFILE=core`. It permits only profile photos and Knowledge & Help images: PNG/JPEG, at most 2 MiB each, bounded dimensions, full decoding, orientation correction, resizing and re-encoding. Original metadata and trailing bytes are discarded. Files remain private and require an authorized app session. The stored status is `image-normalized`, **not** `clean`. This processing is not antivirus scanning. A configured scanner is still used; scanner errors reject the image without fallback.

Payslips, template reference PDFs, project resources/environment docs, official documents and request attachments remain blocked, even when the attachment is a PNG. `PRODUCTION_PROFILE=full` rejects this policy. To enable general uploads later, configure ClamAV and switch back to `UPLOAD_POLICY=scanned`.

1. Push the updated **backend repository** to your GitHub `main` branch and deploy that commit on Render. The old binary does not support this setting. Push/deploy the frontend changes too to show the additional Service connections labels.
2. In Cloudflare, open **R2 Object Storage**, activate it and review the account/billing terms. Create a bucket named `northstar-private` using **Standard** storage. Keep public access and `r2.dev` disabled; no public custom domain is needed.
3. In R2 API token management, create an S3-compatible token with **Object Read & Write** limited to this bucket. Save its Access Key ID, Secret Access Key and account S3 endpoint privately. These are R2 credentials, not a Cloudflare global API key.
4. In Render → backend service → **Environment**, add or update the following. Replace placeholders privately in Render; never put keys in Git, chat or Vercel `NEXT_PUBLIC_*` variables.

```dotenv
APP_ENV=production
PRODUCTION_PROFILE=core
UPLOAD_POLICY=images-only
APP_PUBLIC_URL=https://northstar-intranet.vercel.app
API_PUBLIC_URL=https://northstar-intranet.vercel.app/api
ALLOWED_ORIGINS=https://northstar-intranet.vercel.app
COOKIE_SECURE=true
SEED_DEMO=false
FRONTEND_UPSTREAM_URL=
S3_BUCKET=northstar-private
AWS_REGION=auto
S3_ENDPOINT=https://YOUR_ACCOUNT_ID.r2.cloudflarestorage.com
AWS_ACCESS_KEY_ID=YOUR_R2_ACCESS_KEY_ID
AWS_SECRET_ACCESS_KEY=YOUR_R2_SECRET_ACCESS_KEY
CLAMAV_ADDRESS=
MAIL_DELIVERY=disabled
```

5. Preserve your existing `DATABASE_URL`, `ENCRYPTION_KEY`, admin settings and other unrelated configuration. Save and redeploy. The backend verifies bucket access during startup; incorrect credentials/bucket/endpoint fail startup instead of using local storage.
6. Sign in → My profile → Upload profile photo. Use a PNG or JPEG under 2 MiB, reload, then redeploy the backend and verify the photo is still present. Workspace settings → Service connections should show Storage `s3` (R2 uses this API), Profile and community images enabled, File uploads disabled, Scanner disabled, and Upload policy `images-only`.

The browser sends multipart requests to the frontend `/api` rewrite and the backend accesses R2. No R2 browser CORS rule or public bucket is needed. Keep Vercel `NEXT_PUBLIC_API_URL=/api`, `NEXT_PUBLIC_DEMO_MODE=false`, `NEXT_PUBLIC_COMPANY_SLUG=nexilar`, and `BACKEND_ORIGIN` pointing to your Render origin without `/api`; redeploy Vercel after changing environment variables. Do not redirect file downloads to public R2 URLs.

Cloudflare's Standard free allowance currently includes 10 GB-month storage, one million Class A and ten million Class B operations monthly. Excess usage is billed; this is not a hard $0 spending guarantee. Review billing/usage before enabling R2. If you cannot activate its billing account, leave uploads disabled; no external service is created automatically. [R2 pricing](https://developers.cloudflare.com/r2/pricing/), [S3 setup](https://developers.cloudflare.com/r2/get-started/s3/), [bucket-scoped tokens](https://developers.cloudflare.com/r2/api/tokens/).

### Troubleshooting

| Symptom                                                     | Check                                                                                                                                 |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| Original storage-and-scanning error remains                 | Deploy the new backend commit, verify `UPLOAD_POLICY=images-only` and the live service environment, then redeploy.                    |
| Image-storage error                                         | The bucket is not configured. Set `S3_BUCKET` and the other R2 fields above.                                                          |
| Render startup: storage configuration could not be verified | Bucket name, endpoint, region `auto`, bucket-scoped access key and secret; use the account S3 endpoint, not an R2 public URL.         |
| 400 on an image                                             | Actual PNG/JPEG bytes, less than 2 MiB, non-corrupt, maximum 4096 px per side and 16 megapixels. SVG/GIF/WebP/HEIC are not supported. |
| 401                                                         | Sign in again; verify same-origin `/api` proxy/cookies.                                                                               |
| 503 on a document/resource/payslip                          | Expected in images-only mode; general files need antivirus scanning.                                                                  |
| 422 with a scanner configured                               | Fix scanner availability/verdict; the app does not bypass a failing scanner.                                                          |

## 2. Email invitations and notifications

For now, leave `MAIL_DELIVERY=disabled`. In Workspace settings → invitations, enter the employee's email and role, create the invitation and copy its link to share manually. Employees may use Gmail. They enter their full name once during signup. Select **Employee** for ordinary staff; **Owner** can approve requests and manage payslips, onboarding, benefits, holidays and settings. You can use MFA without an email service: enroll an authenticator in Workspace settings and keep recovery codes privately.

Render Free blocks outbound SMTP ports 25, 465 and 587, so Gmail SMTP on its normal ports will not work there. Resend supports alternate **2465 (implicit TLS)** and **2587 (STARTTLS)**; the existing Go SMTP client supports both. Use one of those ports, not 465/587. [Render restrictions](https://render.com/docs/free), [Resend SMTP](https://resend.com/docs/send-with-smtp).

When you have a domain you control, create a Resend Free account, add the domain, publish the DNS verification records, and generate a sending API key. This does not require Google Workspace, but a sender domain is required for normal employee delivery. Add these in Render and redeploy:

```dotenv
MAIL_DELIVERY=smtp
SMTP_ADDRESS=smtp.resend.com:2465
SMTP_IMPLICIT_TLS=true
SMTP_USERNAME=resend
SMTP_PASSWORD=YOUR_RESEND_API_KEY
SMTP_FROM=Nexilar <notifications@YOUR_VERIFIED_DOMAIN>
```

Alternatively use `smtp.resend.com:2587` with `SMTP_IMPLICIT_TLS=false`; the app requires STARTTLS. Keep your existing encryption key. Create a test invitation to an address you control, check the app's Email delivery job and the provider's delivery log, and test the invite link in a signed-out browser. A queued/sent job means accepted by SMTP, not proof of inbox delivery. Review the provider's free sending quota in its dashboard.

Without a verified sender domain, Resend's `onboarding@resend.dev` sender can only send test messages to the address associated with your Resend account. It cannot invite arbitrary employees. Continue manual links rather than claiming delivery is configured. [Resend test-domain restrictions](https://resend.com/docs/knowledge-base/403-error-resend-dev-domain).

## 3. Google SSO and Google Drive: skip for now

Keep `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` and `GOOGLE_APPLICATION_CREDENTIALS` unset. Leave the company Workspace domain/Drive folder empty. Password login, invitations and authenticator MFA work independently. The app's SSO is Workspace-domain restricted; consumer Gmail login is not substituted. Drive synchronization uses a private Workspace Shared Drive and service account, not a public personal folder. Enable them later with [PRODUCTION.md](PRODUCTION.md); OAuth callback will be `https://northstar-intranet.vercel.app/api/auth/sso/callback`.

## 4. PostgreSQL before 12 November 2026

Your Render screenshot shows that expiry date. Free Render databases expire after 30 days; do not wait until expiration to export data. Your current production database must be preserved, not reset to fix uploads. [Render Free databases](https://render.com/docs/free).

For a continuing free allowance, consider **Neon Free PostgreSQL** in a nearby region. Review current compute/storage/transfer limits before choosing it. The backend polls its durable job queue every five seconds while awake, so Neon may not scale to zero while this Go service is running; Free compute allowance can be exhausted. Render Free also sleeps and pauses background email/deletion work while asleep. This setup does not promise continuous availability. [Neon plans](https://neon.com/docs/introduction/plans).

Migration is a separate planned change, not an upload prerequisite:

1. Create a separate Neon Free project/database and save its direct PostgreSQL URL privately, with the required TLS options. Keep the Render database intact.
2. Arrange a write-free window: company maintenance mode stops ordinary employees, but the owner and background jobs can still write. Stop the backend during the final export/cutover and restrict database access so the snapshot is consistent.
3. Use native PostgreSQL **18 or newer** client tools for this Render PostgreSQL 18 source. Export using `pg_dump --format=custom --no-owner --no-acl` with credentials supplied privately via your local environment/password file. Store the dump outside Git; it contains personal data.
4. Restore into the **empty** Neon database with `pg_restore --no-owner --no-acl --exit-on-error`. Match a compatible target PostgreSQL version; a restore test is required. Do not use `--clean` against a database with real data. Verify table counts, migrations, user login and representative records.
5. Change only Render `DATABASE_URL`, retain `ENCRYPTION_KEY`, restart/deploy and check `/readyz`, real sign-in, requests, MFA, photos and queued work. Reopen the workspace after verification. Do not create a new blank database and expect existing accounts to follow automatically.
6. Retain the old database and private backup through verification; retiring it is a later decision. Restore the old URL for rollback only before accepting new writes, otherwise reconcile those writes first.

This guide does not migrate or delete either database. While retaining Render, use the **Internal Database URL** from the same-region backend and restrict unnecessary public database access after confirming your backup access method.

## ภาษาไทย

ตอนนี้ใช้ Vercel URL ด้านบนและ Render ตามเดิม ยังไม่ต้องเชื่อม Google Workspace/Drive การอัปโหลดรูปต้องสร้าง R2 bucket ส่วนตัว แล้วตั้ง `PRODUCTION_PROFILE=core` และ `UPLOAD_POLICY=images-only` พร้อมค่าจาก R2 ใน Render หลัง deploy โค้ด backend ชุดนี้ รูป PNG/JPEG จะถูกอ่านและสร้างใหม่ก่อนเก็บ ไม่ใช่การสแกนไวรัส ไฟล์เอกสาร สลิป และไฟล์โครงการยังอัปโหลดไม่ได้จนกว่าจะมี ClamAV ห้ามเปิด bucket เป็นสาธารณะหรือใส่คีย์ใน frontend

R2 มีโควตาฟรี แต่ส่วนเกินคิดเงิน ตรวจสอบเงื่อนไขบัญชีก่อนเปิดใช้งาน ถ้ายังไม่มีโดเมนส่งอีเมล ให้สร้างคำเชิญแล้วคัดลอกลิงก์ส่งเอง Resend ต้องยืนยันโดเมนเพื่อส่งให้พนักงานทั่วไป และบน Render Free ใช้พอร์ต 2465 หรือ 2587 แทนพอร์ต SMTP มาตรฐาน เก็บ MFA recovery codes ไว้ส่วนตัว

ฐานข้อมูล Render ในภาพหมดอายุวันที่ 12 พฤศจิกายน 2026 ต้องสำรองและวางแผนย้ายก่อนวันนั้น ห้ามล้างฐานข้อมูลจริงเพื่อแก้ปัญหารูป โปรเจกต์ Neon Free เป็นทางเลือกที่ต้องตรวจสอบโควตาและทดสอบ restore ก่อนเปลี่ยน DATABASE_URL โดยรักษา ENCRYPTION_KEY เดิมไว้
