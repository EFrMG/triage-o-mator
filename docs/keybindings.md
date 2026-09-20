# Keybindings

The TUI's key scheme, agreed 2026-09-19. Every binding is defined once in `tui/keys.go`, which the handlers, the footer and `?` all read, so a change goes there and here together.

**Rules**

1. One key, one meaning, on every screen. Where an action doesn't apply, the key does nothing.
2. A capital letter is the bigger version of the same action (`A`, `R`, `X`, `B`); `G` (bottom) is the only navigation exception.
3. Destructive and bulk actions need the same key twice; any other key cancels, and the status line says what will happen.
4. Item actions work on whatever item is in front of you: open, hovered in a list, a group member, a duplicate candidate.

**Global** (any screen, except while typing in a text field)

| key           | meaning                                                                                                                                                                                                                                                |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `?`           | show/hide key descriptions in the footer                                                                                                                                                                                                               |
| `/`           | search the current list, or the theme picker's                                                                                                                                                                                                         |
| `!`           | the last failure in full: the command and everything it printed                                                                                                                                                                                        |
| `y` / `Y`     | take this screen's context to the clipboard, for pasting to an agent: `y` what is in front of you (the ticked items, the hovered one, the open item), `Y` the whole screen's worth (the list, the batch with its file paths, the group with its notes) |
| `t`           | theme picker                                                                                                                                                                                                                                           |
| `r` / `R`     | fetch changes and sync / full re-fetch, on every screen                                                                                                                                                                                                |
| `q`, `Ctrl-C` | quit, asking first if decisions are unsaved / force quit                                                                                                                                                                                               |
| `Esc`         | back one level                                                                                                                                                                                                                                         |

**Navigation**

| key                                   | meaning                                                            |
| ------------------------------------- | ------------------------------------------------------------------ |
| `j` `k` / `g` `G` / `Ctrl-D` `Ctrl-U` | down, up / top, bottom / half page; `g` is never anything else     |
| `Enter`                               | open or activate the selection                                     |
| `h` / `l` (or `←` / `→`)              | back / open; in an item, between its content and the decision form |
| `H` `L`, `1`–`4`                      | previous / next tab in the item view; jump to a tab by number      |
| `Space`                               | tick an item for a bulk action, in every list                      |

**Item actions**

| key       | meaning                                                                                                                                             |
| --------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Ctrl-S`  | save the decision (works while typing)                                                                                                              |
| `a`       | approve, its only meaning; on ticked items it always needs a second press                                                                           |
| `m` / `M` | mark as duplicate: on an item or pair, opens the comparison; there, marks the hovered candidate a duplicate of the item on top / makes it that item |
| `b` / `B` | add to a group (pick one, write a note) / add to the last group; works on hovered and ticked items                                                  |
| `o`       | open on GitHub                                                                                                                                      |
| `u`       | undo one step, with a second press: take back an approval, or clear an unreviewed decision                                                          |
| `c`       | post a comment (reserved)                                                                                                                           |

**Batches and groups**

| key       | meaning                                                                                                                                                                                                                                                            |
| --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `n`       | new batch / new group                                                                                                                                                                                                                                              |
| `e`       | edit the selection: group details, or a member's note                                                                                                                                                                                                              |
| `d` / `D` | delete or remove the selection, with a second press: a batch, one batch item, a group member, or a whole group from the group list; among duplicates, `d` rules the hovered pair out for good (`bin/not-duplicate`) and `D` clears the handled pairs from the list |
| `A`       | apply the batch's proposals, from the batch list and from inside a batch                                                                                                                                                                                           |
| `x` / `X` | export a group / export it with bodies, comments and diffs                                                                                                                                                                                                         |

**Forms and editors**

| key                      | meaning                                                                                               |
| ------------------------ | ----------------------------------------------------------------------------------------------------- |
| `Tab` / `Shift-Tab`      | next / previous field                                                                                 |
| `J` / `K` on a choice    | next / previous field, like `Tab` / `Shift-Tab`; letters in text fields                               |
| `j` / `k` (or `↑` / `↓`) | on a choice field, change its value                                                                   |
| `l` / `→` on a choice    | open the list of all values; `Enter` or `l` / `→` there pick and move on, `Esc` or `h` / `←` close it |
| `Enter`                  | confirm the field and move to the next; submit on the last (on a choice, it keeps the value)          |
| `Ctrl-S`                 | submit from any field                                                                                 |
| `Esc`                    | cancel                                                                                                |

**Switch Repo**

| key                      | meaning                                                                                    |
| ------------------------ | ------------------------------------------------------------------------------------------ |
| type                     | filter the listed repos; an absolute path opens an install, an `owner/repo` starts it here |
| `Tab` / `Shift-Tab`      | between the text field and the list, keeping the last highlighted repo                     |
| `j` / `k` (or `↑` / `↓`) | move through the list, once one is highlighted; in the text field they are letters         |
| `Enter`                  | open the highlighted repo, or act on what is typed                                         |
| `Esc`                    | back, or quit when no install is open yet                                                  |

On a path with no install, `Enter` shows what installing there would change and writes nothing. On that plan: `Enter` makes exactly those changes, `s` shows the other mode's plan (tracked or solo), `j` / `k` scroll it, `Esc` leaves the repository as it was.
