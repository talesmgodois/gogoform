# 000026 transactional_email

No email exists today. Add an `internal/pkg/mailer` with an SMTP implementation (works with Resend/Postmark/SES SMTP) and a log-only implementation for dev/tests. Config: `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `MAIL_FROM`, `APP_BASE_URL`.

- Email verification on signup: signed, expiring token (store hashed in a `user_tokens` table with purpose + expires_at + used_at); `/app/verify?token=`; unverified users can sign in but see a banner and cannot publish forms.
- Password reset: `/app/forgot-password` and `/app/reset-password?token=` (1h expiry, single use, same response whether the email exists or not).
- New submission notification to the form owner (pro only, per-form toggle).
- Billing emails: payment failed (from the 000024 webhook).
- Plain text + simple HTML templates; send asynchronously so requests don't block on SMTP.

## Tests
- Token lifecycle tests (expired, reused, wrong purpose) and handler tests using the log mailer.

## Note from 000025 (plan limits)
- The per-form submission notification toggle is a pro feature. When enabling it (and before sending), check `plans.For(tenant.Plan).EmailNotifications` (`internal/pkg/plans`); reject free tenants with `apperrors.NewPlanFeatureUnavailable(...)` (`plan_feature_unavailable`, HTTP 403), mirroring the webhooks gate in `cmd/api/handlers/webhooks.go`.
