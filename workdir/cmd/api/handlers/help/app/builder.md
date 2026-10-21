---
section: Workspace
order: 11
---
# Form builder

Build a form visually: add fields from the left, arrange them in the middle
and edit the selection on the right.

## Building a form

Give the form a **title** and an optional **description**, then add fields.
Click a field on the form to select it: the panel on the right shows its
properties (label, name, whether it is required, options…). With nothing
selected, the panel shows the form settings.

## Components

The **Components** tab lists the field types: click one to add it at the end
of the form, or drag it to where you want it. Drag fields on the form to
reorder them.

## Form settings

- **Slug**: the address of the form, `/f/{slug}`. Lowercase letters and
  digits joined by hyphens, unique across GoGoForm. It follows the title until
  you edit it.
- **Public**: anyone with the link can fill the form in. Otherwise only
  signed-in users can, and **Accept anonymous submissions** decides whether
  they are recorded.
- **Create form** / **Save changes** saves the form. New forms are active;
  the schedule and activation are kept when you edit a form.

## Code

The **Code** tab shows the form as the JSON (or YAML) body of the API call
that creates or updates it. Copy it to create the same form from your own
scripts with your [API key](/app/help?doc=app/account).

## Preview and checks

**Preview** shows the form as respondents will see it. The badge next to it
counts the problems to fix before saving, such as a missing title or two
fields with the same name; click it to list them. The download button saves
the form definition as a file.
