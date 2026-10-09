# Vendored shadcn stylesheet

`tailwind.css` is an unmodified copy of `shadcn@4.21.1/dist/tailwind.css` from
the previously locked npm installation. SHA-256:
`4c371f7a1ff5d219ae2f7ff28bd256b4346fd546fe46fbae22092e57db2f0fae`.
MIT license: `../../../licenses/npm__shadcn-4.21.1-LICENSE.md`.

The CLI is not required to build or run copied components. Keeping only this
static stylesheet removes its unpatched braces dependency chain
(GHSA-vfj7-8cjw-p6xm) from normal installs and CI. No vulnerable JavaScript was
copied. Keep this source outside application design-token CSS: it contains
upstream Tailwind utility definitions, not application visual overrides.

To update, review an exact upstream version and its dependency audit first,
extract only the stylesheet from its npm package, preserve the matching license,
and update the checksum and notice. Run the full frontend check and compare the
compiled CSS and rendered components. Do not install the CLI back into the
application or use an unchecked `npx shadcn@latest` as part of CI.
