# Deploy Nexilar v1.0: Render Go + PostgreSQL

This repository deploys independently as a native Go service; the separate frontend goes to Vercel. No Docker or local database upload is needed. Start with a new managed production database. Only the Superadmin is created automatically.

## GitHub

Create an empty private backend repository. In this directory run:

```sh
git add .
git diff --cached --stat
git commit -m "Release Nexilar backend v1.0.0"
git branch -M main
git remote add origin https://github.com/YOUR_ACCOUNT/nexilar-backend.git
git push -u origin main
git tag v1.0.0
git push origin v1.0.0
```

If origin exists, use `git remote set-url origin ...`. Review staged paths: filled environment files, credentials, .local, uploads, binaries, screenshots and database dumps must be absent. Blank environment examples, source, migrations, checksums and explicit synthetic fixtures belong in Git.

## Render creation

First create the frontend Vercel project to obtain its stable production domain. In that project set NEXT_PUBLIC_DEMO_MODE=false, NEXT_PUBLIC_API_URL=/api and NEXT_PUBLIC_COMPANY_SLUG=nexilar. It will not connect until the backend exists.

In Render choose **New → Blueprint**, connect this backend repository, choose main and root **render.yaml**. It creates a native Go web service and managed PostgreSQL in Singapore on paid entry-level plans. Review the displayed charges. The database blocks public access and the backend uses its internal connection. Free Render Postgres expires after 30 days and has no backups; use a paid database for production. [Blueprint reference](https://render.com/docs/blueprint-spec), [free limitations](https://render.com/docs/free).

Enter the prompted values:

| Variable        | Value                                                    |
| --------------- | -------------------------------------------------------- |
| ADMIN_EMAIL     | Your real Superadmin email                               |
| APP_PUBLIC_URL  | `https://YOUR-PROJECT.vercel.app`                        |
| API_PUBLIC_URL  | `https://YOUR-PROJECT.vercel.app/api`                    |
| ALLOWED_ORIGINS | `https://YOUR-PROJECT.vercel.app` without trailing slash |

Render supplies DATABASE_URL/PORT and generates ADMIN_PASSWORD plus a 32-byte base64 ENCRYPTION_KEY. Save these privately from the environment dashboard. The start script adds sslmode=require when absent. [Postgres connection settings](https://render.com/docs/postgresql-creating-connecting), [generated secrets](https://render.com/docs/blueprint-spec).

For manual creation, first create paid Postgres, then a **Go Web Service** in the same region. Copy `.env.production.example` values into Render and supply a unique password/key. Use the internal database URL and disable public database access.

| Setting        | Value                                              |
| -------------- | -------------------------------------------------- |
| Runtime        | Go                                                 |
| Root directory | `.`                                                |
| Build          | `go build -trimpath -o bin/northstar ./cmd/server` |
| Start          | `sh deploy/render-start.sh`                        |
| Health check   | `/readyz`                                          |
| GOTOOLCHAIN    | `go1.27.2`                                         |

The app listens on Render's PORT. [Native Go deployment](https://render.com/docs/deploy-go-gin).

## Connect Vercel

Copy the backend origin (e.g. https://nexilar-api-xxxx.onrender.com). Set **BACKEND_ORIGIN** in the frontend Vercel Production environment and redeploy. Use only the origin, without /api, credentials or query. Vercel rewrites /api/* to Go so session cookies stay on the frontend origin. ALLOWED_ORIGINS must exactly include that frontend origin. Keep FRONTEND_UPSTREAM_URL empty on Render to avoid a proxy loop. [Vercel rewrites](https://vercel.com/docs/routing/rewrites).

Backend /healthz and /readyz should return 200; /readyz checks PostgreSQL. Backend root 404 is normal. Open the frontend and sign in with company **nexilar**, ADMIN_EMAIL and the generated ADMIN_PASSWORD. Change the password in the app, enroll MFA and save recovery codes.

## Fresh Superadmin and core release

Versioned migrations run automatically. With SEED_DEMO=false and no employees, initialization creates one Nexilar company (slug nexilar) and one **Superadmin**, role **owner**, with a bcrypt password hash. This is the existing owner permission set, not a cross-company bypass. No demo users or business records/files are inserted. Redeploys retain data; ADMIN_PASSWORD is only a bootstrap input, not a password-reset mechanism.

The Blueprint sets APP_ENV=production, PRODUCTION_PROFILE=core, secure cookies, encryption, real PostgreSQL and no demo seeds/email previews. Core supports password login/MFA, manual invitation links, requests/approvals, project data, calendars, community text, points, shop and in-app notifications.

SMTP delivery/password-reset emails, Google SSO and Drive remain unavailable until configured. **All uploads stay disabled until both private S3 and ClamAV scanning exist**, including payslips, resources, profile photos and community images. API uploads return 503; Service connections shows storage/uploads disabled. Core does not store production uploads on Render's ephemeral filesystem.

PRODUCTION_PROFILE=full remains the strict default requiring all providers. See [PRODUCTION.md](PRODUCTION.md) for activation. ClamAV must be privately reachable and speak its TCP protocol; a generic HTTP scanner is not interchangeable. Existing native Ubuntu systemd/nginx examples offer an alternative for running ClamAV locally with Go. SMTP uses MAIL_DELIVERY=smtp plus credentials. Google OAuth callback is the frontend origin + /api/auth/sso/callback; service-account JSON belongs in a private hosting secret file.

## Updates

Push reviewed commits to main to deploy. Do not edit applied migration SQL. Enable database backups and test restoration to a separate database. Code rollback does not roll back data. Preserve ENCRYPTION_KEY across redeploys; rotating it breaks existing MFA/encrypted jobs without migration. Give previews a separate database/backend. Do not run destructive/reset or disposable QA scripts against live data.

## ภาษาไทย

นำ backend repository เข้า Render แบบ Blueprint โดยใช้ render.yaml ระบบจะสร้าง Go และ PostgreSQL แยกกัน ไม่ต้องใช้ Docker ตรวจสอบราคาแพ็กเกจแบบเสียเงินก่อนสร้าง ฐานข้อมูลฟรีหมดอายุใน 30 วัน จึงไม่เหมาะกับข้อมูลบริษัท

ใส่อีเมล Superadmin และ URL frontend จาก Vercel ตามตาราง Render จะสร้างรหัสผ่านเริ่มต้นกับ ENCRYPTION_KEY ให้ เก็บไว้ส่วนตัว แล้วนำ URL backend ไปตั้ง BACKEND_ORIGIN ของ Vercel และ Deploy frontend ใหม่ บัญชีเริ่มต้นมีเพียง Superadmin รหัสบริษัท nexilar ไม่มีข้อมูลตัวอย่าง

ระบบ core ใช้ฐานข้อมูลจริง แต่ยังไม่ส่งอีเมลหรือเชื่อม Google จนกว่าจะตั้งค่า การอัปโหลดไฟล์ปิดไว้จนกว่าจะมีทั้ง S3 ส่วนตัวและ ClamAV อย่าใช้ development หรือข้อมูลตัวอย่างเพื่อเลี่ยงข้อกำหนดระบบจริง
