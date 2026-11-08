# The wdx mark

wdˣ: warden, to the power of x. The w and d are drawn as one monoline with
round ends, and the x is raised above the d as an exponent. Everything is
geometry, no typeface, so it renders the same everywhere.

| File | Use |
| --- | --- |
| `wdx-mark.svg` | the mark on a light background; its ink is `currentColor`, so inline it takes the text colour |
| `wdx-mark-light.svg` | the mark on a dark background |
| `wdx-mark-mono.svg` | one colour throughout, where the accent is not allowed |
| `wdx-wordmark.svg`, `wdx-wordmark-light.svg` | the longer lockup, for headers |
| `wdx-tile.svg` | rounded square, for app icons and square avatars |
| `wdx-avatar.svg` | circle, for GitHub and chat avatars |
| `favicon.svg` | the tile with heavier strokes, so it survives 16 px; `site/public/favicon.svg` is a copy |
| `tide-mark.svg`, `tide-mark-light.svg`, `tide-tile.svg` | tide's mark, a terminal window with a prompt chevron and a block cursor in the same construction, for the site and docs; the site draws it from `site/src/components/TideMark.astro` |

The colours are the site's tokens (`site/src/styles/tokens.css`): accent
`#6fd3c1` (`--brand`), light ink `#e9ecf2` (`--ink`), tile `#1e222a` (`--win`).
A change to one of them changes these files too.
