# 000025 plan_limits_and_gates

Depends on 000024 (`tenants.plan`). Single source of truth for plan limits in a new `internal/pkg/plans` package:

| Limit | free | pro |
|---|---|---|
| Forms | 3 | unlimited |
| Submissions per calendar month (all forms) | 100 | 10,000 |
| File storage (sum of files.size_bytes) | 100 MB | 10 GB |
| "Powered by gogoform" badge | shown | hidden |
| Webhooks, CSV/JSON export, email notification on submission | no | yes |

- Enforce on form creation, public submission (count in the current month), and file upload (API and public).
- Exceeded limits return a clear error (HTTP 402 or 403 with an error code like `plan_limit_reached`); the public form page shows "This form is not accepting responses right now" without exposing plan details.
- Gate pro features: webhooks endpoints, exports, and the notification setting.
- `/app` shows usage vs limits (forms, submissions this month, storage) with an upgrade link.
- Wire the badge from 000023 to the plan.

## Tests
- Table-driven tests for every limit on both plans, including the boundary (exactly at the limit).
