# Web design system

## Source of truth

`src/styles/tokens.css` owns colors, typography, spacing, radii, borders, shadows,
layout dimensions, overlay layers, motion and responsive boundaries. `index.css`
only imports the framework, tokens and application styles. `src/styles/app.css`
contains reusable layout rules and consumes tokens; it must not define raw visual
values. Tailwind semantic utilities and application CSS resolve to the same token
values. The former generated light palette and later application override have
been consolidated; `.dark` overrides semantic colors in the same file.

The shared spacing unit is 4px. Typography has three everyday sizes and two
exceptions for headings and key numbers. The default body is `sm` (12px); users
can choose `md` (13px) or `lg` (14px) in Settings. The choice is saved as
`stackharbor.font-size` and applied to `html[data-font-size]` before React renders.
Tailwind utilities and application CSS consume `--type-body` and
`--leading-body`, so ordinary text changes together. The compact table rows,
navigation, header and page gutters leave more room for data.

| Use | Token | Size / line height |
| --- | --- | --- |
| Auxiliary text, table labels, badges, small controls | `--type-sm` / `text-xs` | 12px / 18px |
| Body, table values, menus, ordinary buttons | `--type-body` / `text-sm` | 12, 13 or 14px / 18, 20 or 22px |
| Emphasized text, mobile input text | `--type-lg` / `text-base` | 14px / 22px |
| Section headings and brand | `--type-heading` | 18px |
| Page headings and key statistics | `--type-page`, `--type-stat` | 24px |

Keep ordinary content in the first three sizes. Use color and weight before
introducing another size. Headings and statistics are the only larger exceptions.
This follows Ant Design's guidance to keep non-display typography to 3–5 sizes,
while choosing a denser 12px default for this data-heavy console.

| Change | Token family / example |
| --- | --- |
| Brand, surfaces, text, borders, status | `--primary`, `--card`, `--muted-foreground`, `--destructive` |
| Font sizes, weight, tracking | `--type-sm`, `--type-body`, `--type-lg`, `--font-weight-heading`, `--tracking-table` |
| Padding and gaps | `--spacing`, `--space-3`, `--space-6` |
| Card/control shape | `--radius`, `--radius-panel`, `--control-radius-sm` |
| Shell, tables and overlays | `--sidebar-expanded-width`, `--info-popover-width`, `--dialog-max-width` |
| Layering, shadows, animation | `--z-overlay`, `--shadow-md`, `--motion-fast`, `--motion-layout` |
| Responsive boundaries | `mobile` / `desktop` (640px), `summary-stack` (760px), `compact` (900px) |

Spacing aliases derive from `--spacing` (e.g. `--space-3 = 3 × --spacing`).
Quarter steps preserve existing fine spacing without repeating numbers in pages.
Use an existing size first; add a new size only when it has a distinct purpose.
Structural values such as `0`, `100%`, `1fr`, flex/grid counts and Radix runtime
geometry remain structural CSS. Media queries cannot consume CSS custom
properties, so Tailwind custom variants define their boundaries in the token
file; layouts use `@variant mobile` etc.

## Shared components and coverage

| Component | Responsibility | Consumers |
| --- | --- | --- |
| `Panel` | Surface, border and card radius; compact radius variant | Summary cards, workspace summary, all data lists, plan review sections |
| `SummaryCard` | Named section, total, link, metrics and explanation | Legacy summary composition |
| `DefinitionList` | Semantic `dt`/`dd` entries; details/grid/inline layouts | All entity popovers and summary metrics |
| `InfoPopover` | Trigger, accessibility label, hover timing and portal surface | `WorkspaceInfo`, `ResourceInfo`, `NodeInfo` |
| `DataList` | Filtering, empty states, responsive table; layout variants | Workspaces, resources, services/tasks, operations |
| `MetricValue` | Consistent CPU/memory cells, including unknown values | Resource and node tables |
| Existing UI primitives | Buttons, statuses, alerts, tabs, selects, menus, dialog | All routes and connection/loading/error states |

Entity components keep their own data fields and business labels. Pages choose a
`DataList` layout (`workspaces`, `resources`, `nodes`, default) rather than wrapping
it in page-specific styling containers. Resource data is reused in both the global
Resources page and the workspace Resources tab. The shared shell still provides
navigation, breadcrumbs, connection status and refresh on every route.

The three entity popovers share interaction timing in `InfoPopover`; these are
Radix behavior settings, distinct from CSS transition duration tokens. API polling,
expiry and stale-state timings are business behavior and are not design tokens.

## Adding or changing a page

1. Use existing UI primitives and shared components before creating new markup.
2. Use semantic color utilities (`bg-card`, `text-muted-foreground`, `border-border`)
   and token-backed spacing/type utilities. Application CSS uses `var(--...)`.
3. Put a genuinely new visual value in `tokens.css`; do not add hex colors,
   numeric inline styles or arbitrary pixel/rem utilities to a page.
4. Change shared table density, popover presentation or card layout in the shared
   component/style rather than patching individual routes.
5. Run the checks below. The architecture test rejects raw CSS visual dimensions,
   raw JSX palette/arbitrary size utilities and unresolved application variables.

## Verification

From `web/`:

```sh
rtk npm run typecheck
rtk npm test -- --run
rtk npm run build
rtk node ../scripts/web-assets.mjs validate
```

The browser check uses intercepted fixture API responses and rejects unexpected
mutations. It does not operate on live workspace services. Start a Vite dev or
preview server, then run:

```sh
rtk npm exec -- vite --host 127.0.0.1 --port 5174
rtk node src/test/design-system.browser.mjs /tmp/stackharbor-design-system
```

Set `DESIGN_SYSTEM_URL` to check a different local URL. The script checks nine
routes (Workbench, Workspaces, Resources, Operations, Settings and four workspace tabs) at
1440/900/760/640/390/320px, saves desktop/mobile screenshots, checks document
overflow and runtime errors, and exercises shared popovers, action menus, review
dialogs, focus return and sidebar toggles. It also mutates spacing, typography,
primary color and radius tokens to prove propagation through both application
CSS and utility components, and checks dark surface separation.

The existing React tests continue to cover connection recovery, routing, stale
state, unknown metrics, ownership, log targets and confirmation safeguards. The
browser fixture check is presentation/interaction verification; real session or
backend operations are outside this refactor.

Node action menu items carry the initiating button's focus key. A review may open
before the menu's closing animation ends; the focused menu item then disappears.
Sharing the key lets `PlanDialog` return focus to the current action button after
close, including after a data refresh. The browser check reproduces this path
with screenshots and waits for focus restoration rather than assuming it is
synchronous.

## Console preferences

The sidebar footer links to `/settings`, including in the collapsed desktop
layout and mobile navigation. `PreferencesProvider` owns English/Chinese,
system/light/dark appearance and text-size choices, saved under `stackharbor.language`,
`stackharbor.theme` and `stackharbor.font-size`.
System appearance is the default when no valid choice is stored; it follows
`prefers-color-scheme` changes while selected. Explicit light/dark choices
ignore system changes. Changes apply immediately; unavailable storage still
permits in-session changes.
`lib/translations.ts` holds Chinese interface text. Translate labels at render
boundaries and keep route keys, protocol values, workspace identities and log
content unchanged. Date formatting follows the selected language.

The settings route can render before inventory discovery completes. The browser
check also covers its footer placement, keyboard input, reload persistence and
light/dark layouts at desktop and mobile sizes.

## Workbench

The root route is Workbench / 工作台. `pages/Workbench.tsx` composes shadcn
Card and Chart (Recharts) with the existing tokens. `lib/workbench.ts` derives
snapshot statistics: unique workspaces, disjoint fresh/attention/unavailable
session counts, shared resources deduplicated by session/node reference, and
the top five measured physical resources by memory. Unknown, non-finite and
negative samples are excluded; a measured zero remains valid. No historical
trend or host-capacity percentage is inferred from snapshot data.

Chart values are also available as visible text. Charts disable animation,
retain keyboard focus indication, and use the selected language for labels.
Metric and session links lead to existing inspection flows. Runtime controls
continue to require the existing plan review. The Workbench module is lazy
loaded so other routes do not eagerly load the chart dependency.

## Navigation vocabulary

Top-level navigation is Workbench / 工作台, Workspaces / 工作区,
Runtime resources / 运行资源, and Operations / 操作记录. Workbench summarizes
current state; Workspaces organizes session-level inspection; Runtime resources
shows physical processes and containers across workspaces. Operations includes
both in-progress actions and completed outcomes, so it is not labeled History.
The Workspaces navigation count explicitly uses session units. Within a
workspace, the scoped Resources / 资源 tab retains its shorter label. Existing
URL paths remain unchanged.
