# Themes

Each `themes/*.json` file defines one complete palette. The filename without `.json` is its stable ID (for example, `nord`). The TUI reads these files whenever the theme picker opens.

Press `t` outside text fields to open the theme list. Move with `j/k` or arrow keys to preview a palette, or `/` to search the names; `Enter` applies it and saves its ID in gitignored `config/theme.local`. `Esc` or `h` restores the previous palette without changing the saved preference (after a search, the first `Esc` clears it). The picker preserves your current item, group, and unsaved decisions.

`TRIAGE_THEME=<id>` overrides the saved preference at startup. Selecting a different theme still works for the current session.

## Custom palettes

Copy any palette to another file in this directory, change its display name and colors, and reopen the picker. Use lowercase letters, digits, hyphens, or underscores in the filename.

```json
{
  "name": "Custom Theme",
  "background": "#1e1e2e",
  "foreground": "#cdd6f4",
  "muted": "#a6adc8",
  "border": "#585b70",
  "accent": "#cba6f7",
  "selection": "#313244",
  "success": "#a6e3a1",
  "warning": "#f9e2af",
  "error": "#f38ba8",
  "info": "#89b4fa"
}
```

The display `name` is optional; the first nine color roles are required and all of them must be `#RRGGBB` values. `info` is optional and falls back to `accent`, so palettes written before it keep loading; the bundled palettes keep the two apart, so a `[human]` mark doesn't look like focus. Unknown fields, invalid colors, missing roles, and malformed JSON report an error naming the file. A failed picker reload leaves the current theme unchanged. A malformed catalog at startup fails with the same actionable error.

| Role         | Used for                                       |
| ------------ | ---------------------------------------------- |
| `background` | Screen and panel backgrounds                   |
| `foreground` | Normal text                                    |
| `muted`      | Secondary text and placeholders                |
| `border`     | Inactive borders                               |
| `accent`     | Active borders, fields, and selections         |
| `selection`  | Highlighted row backgrounds                    |
| `success`    | Successful status and saved indicators         |
| `warning`    | Pending work, unsaved changes, `[agent]` marks |
| `error`      | Error messages                                 |
| `info`       | The palette's blue: `[human]` marks            |

## Bundled palettes and upstream sources

The bundled files map these projects' palette colors to this application's ones:

- [Catppuccin](https://github.com/catppuccin/palette): Latte, Frappé, Macchiato, Mocha (default).
- [Tokyo Night](https://github.com/folke/tokyonight.nvim): Night, Storm, Moon, Day.
- [Dracula](https://github.com/dracula/dracula-theme#color-palette-oss).
- [Nord](https://github.com/nordtheme/nord/blob/develop/src/nord.scss).
- [Gruvbox](https://github.com/morhetz/gruvbox): Dark, Light.
- [Rosé Pine](https://github.com/rose-pine/palette): Rosé Pine, Moon, Dawn.
