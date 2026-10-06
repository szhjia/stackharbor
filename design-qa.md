# Brand and content-heading QA

Source visual truth: `/Users/kimi/.codex/generated_images/01a10fdc-59eb-7843-91ad-43752a070d5c/exec-55813a4b-d5f9-4528-a0d3-73da4abd1744.png` (selected option 1, 1254 × 1254).

Implementation: `http://127.0.0.1:16800/`, live inventory, desktop viewport 1096 × 954, screenshot at 1 CSS pixel per image pixel. Evidence directory: `/Users/kimi/.codex/visualizations/2026/10/06/01a10fdc-59eb-7843-91ad-43752a070d5c/stackharbor-audit/`.

- Browser screenshot: `18-brand-and-content.jpg` (1096 × 954).
- Workspace screenshot: `19-brand-workspace.jpg`.
- Full comparison: `21-design-comparison.jpg` (1600 × 1000), reference board downsampled to 480 px wide beside browser capture. The board is a brand reference, not a page-layout specification.
- Focused comparison: `22-logo-comparison.jpg` (800 × 320), reference dark symbol beside an enlarged browser brand crop. Enlargement is for shape inspection, not sharpness assessment.

## Findings

No actionable P0/P1/P2 differences in the scoped brand integration and heading simplification.

- Typography: existing readable sidebar wordmark retained as live text, horizontal lockup adapted to the existing sidebar. Current route has one semantic h1 in AppHeader; section headings retain h2 hierarchy.
- Spacing: symbol is 32 × 32 CSS px with a 12 px wordmark gap. Content starts directly with useful data; workspace path remains visible. No duplicate content title or eyebrow.
- Colors: mint and teal stack with white angular dock on the dark sidebar; navy dock on white favicon, consistent with selected option 1.
- Image fidelity: generated production assets preserve three stacked modules and bevelled dock. Transparent sidebar PNG is 183 × 183; favicon PNG is 256 × 256. The source mark was normalized for padding and downsampled for export. No SVG/CSS replacement drawing. Both assets load successfully; favicon returns HTTP 200 image/png with hashed cache-safe URL.
- Copy: StackHarbor and LOCAL CONTROL preserved. Removed generic repeated page introductions; retained actual workspace path and functional section labels.

## Interaction verification

Overview, Workspaces, Resources, Operations and workspace detail render the shared header. Workspace breadcrumb returns to the list. Routes and deep-link behavior pass the existing automated tests. Browser console has no error entries after update.

At 390 × 844 CSS px, DOM geometry confirms main/sidebar width 390 and document scrollWidth 390. The screenshot provider scales the viewport capture within its larger surface, so `20-brand-mobile.jpg` is not used to claim pixel-level mobile visual fidelity.

Comparison history: first comparison passed; no visual fixes were made in response. Selected design is intentionally adapted to a horizontal sidebar lockup rather than displaying the design board.

## Implementation checklist

- [x] Selected first logo applied to sidebar and connection screen.
- [x] Matching favicon bundled with hashed URL.
- [x] Duplicate page heading component removed across routes.
- [x] One h1 in AppHeader; workspace path retained.
- [x] 27 frontend tests, typecheck, production build and asset validation passed.
- [x] Local console rebuilt, restarted, and checked in browser.

Follow-up polish: none required for this scope. Browser chrome favicon rendering at individual 16/32 px sizes was not captured; asset delivery and selected shape were verified.

final result: passed
