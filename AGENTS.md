# Backend contributor rules

- Read README.md and docs/API.md. Parent workspace docs are optional; this repository must work independently.
- Run gofmt, go test ./..., and go vet ./... after changes. Use TEST_DATABASE_URL for repository integration work.
- Domain validation and authorization live in internal/domain. Never trust client hours, leave days, role, or approval permissions.
- Keep sessions hashed and password hashes out of JSON. Payslips and documents are authenticated, private files.
- Use transactional writes for cross-record invariants. Do not remove the balance/overlap/allocation protections.
- Keep changes in schema.sql idempotent; add versioned migrations for future schema changes. Never erase existing production data.
- No Docker. Preserve native startup and deployment.
