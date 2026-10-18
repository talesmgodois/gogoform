# 000029 legal_pricing_and_account_pages

Public product pages (server-rendered templates sharing one layout):

- `/` landing page: what gogoform does, screenshots/feature list, call to action to sign up.
- `/pricing`: Free vs Pro (9 USD/month) comparison using the limits from `internal/pkg/plans` (000025) so the page never drifts from enforcement; buttons go to signup or checkout.
- `/terms` and `/privacy`: placeholder legal text clearly marked to be reviewed, mentioning Stripe as payment processor and that form responses are stored on behalf of the form owner.
- Footer with links to pricing, terms, privacy and contact email (config `SUPPORT_EMAIL`).

Account settings `/app/account` (replace the current debug route list):
- Change email (re-verification) and password (requires current password).
- Show API key with copy and rotate (from 000021).
- Delete account: confirmation by typing the email; cancels the Stripe subscription, deletes the tenant data.
