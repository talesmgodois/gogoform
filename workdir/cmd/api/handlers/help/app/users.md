---
section: Administration
order: 30
---
# Users

Admins only: every user account, newest first.

## Roles

- **ADMIN** sees every tenant and this page.
- **FORM_CREATOR** and **BASIC** use their own workspace.

New accounts get the BASIC role. Change a role with the API,
`PUT /users/{id}/role`; you cannot change your own.

## Status

Inactive users cannot sign in.
