# Vitamux iOS design

The visual spec of the iOS app ([J22.24](../plan/E22-ios-app/J22.24-design-pass.md)), written from the artboards of the design canvas "Vitamux iOS Screens" (SignIn, Dashboard, Customize in the light theme, Explore, Metric detail, Rule lens, Sleep, ECG, Sources, Apple Health, Lab review, More, Widgets, Tab bar). Status: approved via the design canvas; the owner confirms the restyled app. It is the iOS twin of the panel's [design system](frontend.md#design-system); behaviour and structure stay in [ios-app](ios-app.md).

Rules: stock navigation (tab bar, `NavigationStack`, sheets, lists and forms), Dynamic Type, 44-point targets, and no theme system beyond one flat token file. Colour names a metric, a source or a data state, never whether a value is good.

## Tokens

Surfaces, text, accent and feedback are colour sets with a light and a dark value in `apple/VitamuxApp/Sources/Shared/Tokens.xcassets`, named once in `Sources/Shared/Tokens.swift` (`Color.ground`, …; compiled into the app and the widget). Metric, source, stage and status colours are light/dark pairs in VitamuxKit's [`ChartPalette.swift`](../../apple/VitamuxKit/Sources/VitamuxKit/Charts/ChartPalette.swift), shared with the charts. Dark values are the artboards'; light values are the Customize artboard's and the panel's `tokens.css`.

| Token | Dark | Light | Use |
| --- | --- | --- | --- |
| `ground` | `#0a0b0d` | `#f4f7f7` | Behind every screen and sheet |
| `cardTop` → `cardBottom` | `#16191c` → `#111316` | `#ffffff` | Card fill, top to bottom |
| `cardBorder` | `#23272c` | `#dce4e4` | Card hairline |
| `hairline` | `#1c2024` | `#dce4e4` | Separators inside a card |
| `raised` | `#1b1f23` | `#e7eeee` | Chips, segmented tracks, run-strip gaps |
| `ink` / `inkMuted` / `inkFaint` | `#eef1f3` / `#a1a9b1` / `#868f97` | `#0e1a1b` / `#4f6363` / `#5f7373` | Text: primary; labels, units, sub-lines; section headers, axes |
| `accent` / `accentSoft` / `onAccent` | `#2dd4bf` / `#0f2321` / `#042f2c` | `#0f766e` / `#e0f2ef` / `#ffffff` | Tint, links, the app mark; text on a filled button |
| `feedbackOK` / `Warn` / `Error` / `Info` | `#34d399` / `#fbbf24` / `#fb7185` / `#7cb4ff` | `#047857` / `#b45309` / `#b42318` / `#1d4ed8` | Connection health, alerts, errors, notices (always with a shape and a word) |
| `feedbackWarnSoft` / `ErrorSoft` / `InfoSoft` | `#2b2210` / `#2e151b` / `#142033` | `#fef3c7` / `#fdecea` / `#e8eefc` | Alert banner fills |
| `draft` / `draftSoft` | `#d8ccff` / `#211a33` | `#5b3fc4` / `#eee8ff` | Rule lens, custom-rule pill |

The asset catalogue also holds `AccentColor` (the same teal, for system controls) and the app icon.

**Metric hues** (`MetricHue.color` on `MetricHue.tint`, the icon tile): heart `#fb7185`/`#2e151b`, HRV `#a78bfa`/`#211a33`, activity `#34d399`/`#0f2a20`, sleep `#818cf8`/`#1b1d36`, blood pressure `#f472b6`/`#2e1526`, SpO₂ `#22d3ee`/`#0f2a30`, body `#fbbf24`/`#2b2210`, energy `#fb923c`/`#2e1c10`, VO₂ `#2dd4bf`/`#0f2321`, lab `#a3e635`/`#1f2a10`, other `#94a3b8`/`#1b1f23` (dark). The light theme uses the panel's darker hue on its pale tint (heart `#be123c` on `#ffe4e6`, …).

**Sources** (`SourceStyle.color`, the dot beside every source name): Apple Health `#ff8fb3`, WHOOP `#ffa463`, Withings `#a3e635`, Garmin `#8c9eff`, manual and push `#a1a9b1`, others one of three by a stable hash; darker shades in the light theme. Overlay series also differ by dash.

**Sleep stages:** deep `#6366f1`, light `#38bdf8`, REM `#c084fc`, awake `#71717a` (darker deep, light and REM in the light theme).

**Type:** SF Pro (the system font), tabular figures for values. The iOS scale on Dynamic Type text styles: 34 large title (tab roots), 28 title (detail screens), 17 body and headline, 15 subheadline (labels, sub-lines), 13 footnote (deltas, legends, section headers in capitals tracked 1 pt). Values (`ValueText`, bold, tracking −2%, scaled with Dynamic Type from): 44 metric detail, 40 large dashboard card, 34 widget, 30 dashboard card, 20 stat tile. Tab labels 10 pt.

**Sizes:** icon tiles (`IconTile`, `MetricTile`) 44 in rows and cards, 40 in settings rows, 56 in screen headers, 34 in the artboard's widgets (28 in the shipped widgets, which also carry the "As of" line); corner 28% of the side. Tab glyphs 30 pt in the artboard; the stock tab bar keeps its own size. Radii: card 18, list group 16 (stock inset-grouped), alert and stat tile 14, field and full-width button 12, segmented 10, chips are capsules, widgets the system's. Spacing on a 4-point grid: gutter 16, card padding 14, blocks 14 apart, grid gap 12, chip gap 8.

## Surfaces

- **Ground** behind every route (`RouteView` applies `screenBackground()`) and every sheet (`sheetBackground()`): lists and forms hide their own background, so their stock inset-grouped rows sit on the ground like the artboards' card groups, keeping swipe actions, editing, separators and Dynamic Type stock.
- **Cards** (`CardBackground`, `.card()`): the dashboard's metric and source cards, sign-in fields and notes, stat tiles. Light theme: white on the pale ground with the border carrying the edge.
- **Section headers** (`SectionHeader`) above custom card stacks; list sections keep the stock header.
- **Tab bar:** stock, the selected tab in `ink` and the others in the system's secondary, as the artboard; content inside each tab is tinted `accent`.
- **Buttons:** stock styles tinted `accent`; a filled (`borderedProminent`) button sets its label in `onAccent`, so dark-theme text on teal stays readable. Selected chips and segments are an `ink` pill on `raised`.

## Status and source cues

`Sources/Shared/StatusChips.swift`:

- **Data status** (`DataStatus`, from the kit): direct circle (`accent`), fallback diamond, calculated triangle, overridden square, partial half circle, no data ring; always with its word (`StatusLabel`, the cards' status mark). Colours from `ChartPalette`.
- **App feedback** (`StatusKind`, `StatusIcon`, `HealthBadge`): ok check, warn triangle, error octagon, pending clock, off minus, info; `feedback*` colours and a word. Alerts are a soft fill with the same colour as text and a hairline at 30%.
- **Sources:** `SourceDot` (8 pt), `SourceChip` (dot and name), `SourcePill` (dot and name on `raised`, the first one in `ink`), `UnofficialBadge` (a neutral pill).

## Charts

Swift Charts with the kit's grammar ([ios-app › Charts](ios-app.md#charts)), on the screen's rows and cards. Marks in the metric hue (the darker shade in the light theme), the band at 14% of it, overlays in source colours with dashes, stage colours for sleep, status glyphs on non-direct points, and the selection callout on the system material. Axes and grid stay Swift Charts' system styles, which follow the theme. Sparklines use the hue.

## App icon

`Sources/Assets.xcassets/AppIcon`: the sign-in mark's pulse line in `#2dd4bf` with a soft glow, on the card gradient (`#16191c` → `#0a0b0d`), single 1024-point images for the default, dark (line on transparent) and tinted (white line) appearances. Drawn by a script from the same path as the mark; no third-party art.

## Widgets

The widget extension compiles `Tokens`, `Card` and `MetricTile`: the container is the card gradient, values use `ValueText` (34 small and medium), labels and units `inkMuted`, tiles in the metric palette, sleep stages as one bar, the status glyph only. Lock-screen families stay stock and monochrome. Values remain `privacySensitive`; the signed-out state shows "Open Vitamux to sign in" without values.

## Screenshots

`docs/images/ios/` from the fake server's synthetic data: `apple/VitamuxApp/scripts/screenshots.sh` runs `ScreenshotUITests` with `TEST_RUNNER_SCREENSHOT_DIR`, a 9:41 status bar and the dark appearance, ending with the Customize sheet in the light theme, and shrinks each PNG to 390 points wide.
