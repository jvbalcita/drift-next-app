# Drift visual system

## Direction

**Swiss Editorial Operations** — a warm paper-and-ink control surface for long operational sessions. The interface uses strict alignment, visible rules, square geometry, and restrained signal color instead of neon, glass, or decorative elevation.

## Product intent

Drift is a read-only device-automation control plane during bootstrap. The UI should make fleet state, exceptions, timestamps, and readiness legible at a glance without implying that disabled demo actions are live.

## Tokens

| Role | Value | Use |
| --- | --- | --- |
| Paper background | `#f7f4ee` | App canvas |
| Card paper | `#fffdf9` | Content surfaces |
| Ink | `#20242b` | Primary text |
| Muted ink | `#5b6674` | Supporting text |
| Rule | `#d6d4cc` | Borders and separators |
| Cobalt | `#1f4da8` | Primary action, selected state, links |
| Amber | `#c6811c` | Attention and paused/non-ready states |
| Green | `#16836a` | Healthy/ready states |
| Red | `#b42318` | Destructive/error states |

## Typography

- Inter/system sans for interface text and headings.
- JetBrains Mono/system mono for timestamps, percentages, IDs, status labels, and other tabular telemetry.
- Headings use tight tracking; section labels use uppercase mono with measured tracking.

## Layout and components

- Use visible borders and separators; do not rely on whitespace alone.
- Use zero-radius geometry for cards, buttons, inputs, tabs, and menus. Keep status dots and avatars circular when their semantics require it.
- Prefer flat surfaces. Do not add glow, gradients, glassmorphism, or decorative shadows.
- Use a restrained editorial grid spine around primary content and consistent alignment across metric, fleet, inspector, activity, and readiness sections.
- Preserve shadcn/sidebar-07 composition and responsive Sheet behavior; change visual tokens and class composition rather than replacing primitives.

## Accessibility

- Keep normal text at least 4.5:1 contrast against its surface.
- Preserve visible keyboard focus rings on every interactive control.
- Do not communicate device state by color alone; retain text labels such as Online, Attention, Offline, Ready, Planned, and Disabled.
- Keep the mobile navigation sheet free of backdrop blur so content boundaries remain predictable.

## Implementation boundary

The bootstrap remains mock-only and read-only. Visual work must not introduce ADB, device control, credentials, production synchronization, or live control-plane calls.
