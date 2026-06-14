---
name: Obsidian Precision
colors:
  surface: "#111317"
  surface-dim: "#111317"
  surface-bright: "#37393e"
  surface-container-lowest: "#0c0e12"
  surface-container-low: "#1a1c20"
  surface-container: "#1e2024"
  surface-container-high: "#282a2e"
  surface-container-highest: "#333539"
  on-surface: "#e2e2e8"
  on-surface-variant: "#b9cacb"
  inverse-surface: "#e2e2e8"
  inverse-on-surface: "#2f3035"
  outline: "#849495"
  outline-variant: "#3b494b"
  surface-tint: "#00dbe9"
  primary: "#dbfcff"
  on-primary: "#00363a"
  primary-container: "#00f0ff"
  on-primary-container: "#006970"
  inverse-primary: "#006970"
  secondary: "#ffdb9d"
  on-secondary: "#412d00"
  secondary-container: "#feb700"
  on-secondary-container: "#6b4b00"
  tertiary: "#fff5de"
  on-tertiary: "#3b2f00"
  tertiary-container: "#fed639"
  on-tertiary-container: "#715d00"
  error: "#ffb4ab"
  on-error: "#690005"
  error-container: "#93000a"
  on-error-container: "#ffdad6"
  primary-fixed: "#7df4ff"
  primary-fixed-dim: "#00dbe9"
  on-primary-fixed: "#002022"
  on-primary-fixed-variant: "#004f54"
  secondary-fixed: "#ffdea8"
  secondary-fixed-dim: "#ffba20"
  on-secondary-fixed: "#271900"
  on-secondary-fixed-variant: "#5e4200"
  tertiary-fixed: "#ffe179"
  tertiary-fixed-dim: "#eac324"
  on-tertiary-fixed: "#231b00"
  on-tertiary-fixed-variant: "#554500"
  background: "#111317"
  on-background: "#e2e2e8"
  surface-variant: "#333539"
typography:
  headline-lg:
    fontFamily: Inter
    fontSize: 24px
    fontWeight: "600"
    lineHeight: 32px
    letterSpacing: -0.02em
  headline-md:
    fontFamily: Inter
    fontSize: 18px
    fontWeight: "600"
    lineHeight: 24px
    letterSpacing: -0.01em
  body-md:
    fontFamily: Inter
    fontSize: 14px
    fontWeight: "400"
    lineHeight: 20px
  body-sm:
    fontFamily: Inter
    fontSize: 12px
    fontWeight: "400"
    lineHeight: 16px
  data-lg:
    fontFamily: JetBrains Mono
    fontSize: 16px
    fontWeight: "500"
    lineHeight: 24px
  data-md:
    fontFamily: JetBrains Mono
    fontSize: 13px
    fontWeight: "500"
    lineHeight: 18px
  data-sm:
    fontFamily: JetBrains Mono
    fontSize: 11px
    fontWeight: "400"
    lineHeight: 14px
  label-caps:
    fontFamily: Inter
    fontSize: 10px
    fontWeight: "700"
    lineHeight: 12px
    letterSpacing: 0.06em
rounded:
  sm: 0.125rem
  DEFAULT: 0.25rem
  md: 0.375rem
  lg: 0.5rem
  xl: 0.75rem
  full: 9999px
spacing:
  unit: 4px
  container-padding: 16px
  gutter: 8px
  panel-gap: 1px
---

## Brand & Style

The design system is engineered for high-frequency trading and professional financial analysis. The brand personality is clinical, authoritative, and high-performance, evoking the feel of a premium, mission-critical instrument.

The aesthetic is **Technical Minimalism** with a focus on high-density information architecture. It utilizes a "Midnight Slate" and "Obsidian" palette to reduce eye strain during long sessions while maintaining a sophisticated, premium feel. Visual interest is driven by functional data visualization rather than decorative elements. The UI employs "Hyper-Cyan" for primary actions and "Soft-Amber" for warnings or secondary alerts, creating a high-contrast but controlled environment. Ultra-thin 1px borders define the structure, moving away from heavy shadows toward a precision-milled, hardware-inspired interface.

## Colors

This design system utilizes a tiered dark palette to establish depth without relying on traditional drop shadows.

- **Obsidian (#020203):** The base canvas color, used for the main background to maximize contrast with data.
- **Midnight Slate Series:** Used for containers and surface elevations to create a "panel-based" interface.
- **Hyper-Cyan (#00F0FF):** A vibrant, high-energy primary color used exclusively for active states, primary buttons, and successful trend data.
- **Soft-Amber (#FFB800):** Used for warnings, pending states, and mid-range alerts.
- **Functional Grays:** A range of cool-toned grays (Slate) for secondary text and 1px borders to maintain a technical, low-noise environment.

## Typography

The typography strategy optimizes for rapid legibility and high information density.

- **Inter** is the primary typeface for all UI labels, navigation, and body copy, chosen for its neutral, modern appearance and excellent screen performance.
- **JetBrains Mono** is utilized for all numerical data, price tickers, and timestamps. The monospaced nature ensures that fluctuating numbers do not cause layout shifts and remain perfectly aligned in data tables.
- **Scales:** Typography is kept intentionally small (mostly 11px - 14px) to accommodate the dense nature of trading dashboards. All headings are tight and functional.

## Layout & Spacing

This design system uses a **Technical Grid** model. Rather than fluid whitespace, the layout is driven by "Panels" separated by 1px borders or 8px gutters.

- **Top-Docked Navigation:** All primary navigation is moved to a slim (48px height) top bar to maximize vertical screen real estate for charts and order books.
- **Information Density:** A 4px base spacing unit allows for tight packing of UI elements.
- **Layout Model:** A fluid-width dashboard that utilizes CSS Grid to create multi-pane viewports. On mobile, panels stack vertically, with priority given to the live chart and execution module.

## Elevation & Depth

Depth is achieved through **Tonal Layering** and **Subtle Blurs** rather than shadows.

- **Borders over Shadows:** 1px borders using `border_subtle` create the primary definition between UI zones.
- **Backdrop Blurs:** Modals and dropdown menus use a high-density background blur (20px+) with a semi-transparent `surface_slate_low` fill to create a "glass-on-obsidian" effect.
- **Active States:** High-performance "glow" effects (very low spread, high intensity cyan) are used sparingly to indicate active selections or "hot" data points.

## Shapes

The shape language is "Soft-Square." While 0px radius is too aggressive, a 4px (Soft) radius provides a precise, engineered feel.

- **Standard Radius:** 4px for buttons, input fields, and panels.
- **Outer Containers:** 8px for top-level window frames.
- **Interactive Elements:** Checkboxes and status indicators maintain a strict 2px radius to preserve the technical aesthetic.

## Components

- **Buttons:** Primary buttons are `Hyper-Cyan` with black text for maximum contrast. Secondary buttons are ghost-style with 1px `border_subtle`.
- **Top-Docked Nav:** A slim horizontal bar featuring `label-caps` for navigation links. Active states are indicated by a 2px top-border in `Hyper-Cyan`.
- **Data Tables:** High-density rows (24px - 28px height). Alternating row highlights are not used; instead, 1px horizontal dividers separate entries.
- **Input Fields:** Integrated into panels with 1px borders. Focused states use a 1px `Hyper-Cyan` border—no outer glow.
- **Chips/Badges:** Small, rectangular badges with `data-sm` text. Backgrounds are low-opacity versions of the status color (e.g., 10% Cyan for "Buy").
- **Order Book:** Utilizes the monospace font for all price/size columns with "Soft-Amber" for ask and "Hyper-Cyan" for bid, using horizontal bar-graphs in the background to show depth.
