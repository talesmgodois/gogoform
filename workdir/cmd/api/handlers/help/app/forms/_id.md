---
section: Workspace
order: 12
---
# A form and its submissions

This page shows one of your forms: its status, how to share it and the
submissions it received.

## Form actions

- **Edit** opens the form in the builder; **Duplicate** starts a new form
  from a copy.
- **Activate**/**Deactivate** opens or closes the form to new submissions.
- **Open form** and **Copy public link** give you the link to share.
- **Delete** removes the form, unless it already has submissions or webhooks.

## Exports

**Download CSV** and **Download JSON** export every submission of the form,
not only the page shown. Exports are part of the Pro plan.

## Email notifications

On the Pro plan, tick **Email me when this form receives a submission** to
get an email for every new submission.

## Embed

Paste the snippet into any web page to show the form inside it. The embedded
form keeps working when you edit it.

## Submissions

The submissions, newest first, in pages. **Table** shows one column per
field; **JSON** shows the raw data. Uploaded files link to their viewer.

## Webhooks

On the Pro plan, webhooks post each new submission to a URL of yours, so you
can feed it into other tools. Manage them with the API,
`POST /forms/{id}/webhooks`, using your [API key](/app/help?doc=app/account).
