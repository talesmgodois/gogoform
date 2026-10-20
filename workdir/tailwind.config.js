// Tailwind CSS v3 configuration. `make css` (and the css stage of the
// Dockerfile) compiles tailwind/input.css into
// cmd/api/handlers/templates/static/app.css, which the server embeds and
// serves at /static/app.css. Only classes found in the files below end up in
// the stylesheet: build class names from whole strings, never by
// concatenation, or they will be missing.
/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    './cmd/api/handlers/templates/**/*.{html,js}',
    './cmd/api/handlers/**/*.go',
  ],
  theme: {
    extend: {},
  },
  plugins: [],
};
