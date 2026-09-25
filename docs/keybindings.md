# Keybindings

The TUI's key scheme, agreed 2026-09-19. Every binding is defined once in `tui/keys.go`, which the handlers, the footer and `?` all read, so a change goes there and here together.

Printable keys remain text while editing a text field. Destructive actions and bulk decision changes need the same key twice, except dismissing one row in Notifications with `d`; another key cancels the confirmation. Item actions apply to ticked items, or to the item in front when nothing is ticked.

**General browsing** (outside text fields and modal menus)

| key           | meaning                                                                                                                                                                                                                                                |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `?`           | show/hide key descriptions in the footer                                                                                                                                                                                                               |
| `/`           | search the current list, or the theme picker's                                                                                                                                                                                                         |
| `!`           | the last failure in full: the command and everything it printed                                                                                                                                                                                        |
| `y` / `Y`     | take this screen's context to the clipboard, for pasting to an agent: `y` what is in front of you (the ticked items, the hovered one, the open item), `Y` the whole screen's worth (the list, the batch with its file paths, the group with its notes) |
| `t`           | theme picker                                                                                                                                                                                                                                           |
| `r` / `R`     | fetch changes and sync / full re-fetch                                                                                                                                                                                                                 |
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

`f` opens Local dataset. There, `d` downloads or updates the open backlog, `r` resumes, `n` changes the item limit, `u` measures cache size, `y` copies the agent prompt, and `x` stops the current operation. `j`/`k` scrolls the status; `Esc`/`h` closes the menu without stopping. Repository switches and TUI exit cancel owned corpus processes. See [dataset download](evidence.md#download-from-the-tui).

| key       | meaning                                                                                                                                             |
| --------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| `s`       | save the decision for review; leave the reason field with `Tab` first                                                                               |
| `S`       | save and approve the decision in the open item or duplicate comparison; leave the reason with `Tab` first; records local human review only          |
| `a`       | approve, its only meaning; on ticked items it always needs a second press                                                                           |
| `m` / `M` | mark as duplicate: on an item or pair, opens the comparison; there, marks the hovered candidate a duplicate of the item on top / makes it that item |
| `b` / `B` | add to a group (pick one, write a note) / add to the last group; works on hovered and ticked items                                                  |
| `o`       | open on GitHub                                                                                                                                      |
| `u`       | undo one step, with a second press: take back an approval, or clear an unreviewed decision                                                          |
| `c`       | post a comment (reserved)                                                                                                                           |

`s` and `S` remain letters in text fields. `Ctrl-S` remains an optional save alias, including while typing; saving and approving a decision does not require Ctrl.

**Batches and groups**

| key       | meaning                                                                                                                                                                                                                                                                  |
| --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `n`       | new batch / new group                                                                                                                                                                                                                                                    |
| `e`       | edit the selection: group details, or a member's note                                                                                                                                                                                                                    |
| `d` / `D` | delete or remove the selection, with a second press: a batch, one batch item, a group member, or a whole group from the group list; among duplicates, `d` records the hovered pair as not duplicate (`bin/not-duplicate`) and `D` clears the handled pairs from the list |
| `A`       | apply the batch's proposals, from the batch list and from inside a batch                                                                                                                                                                                                 |
| `x` / `X` | export a group / export it with bodies, comments and diffs                                                                                                                                                                                                               |

**Forms and editors**

| key                      | meaning                                                                                                                         |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------- |
| `Tab` / `Shift-Tab`      | next / previous field                                                                                                           |
| `J` / `K` on a choice    | next / previous field, like `Tab` / `Shift-Tab`; letters in text fields                                                         |
| `j` / `k` (or `↑` / `↓`) | on a choice field, change its value                                                                                             |
| `l` / `→` on a choice    | open the list of all values; `Enter` or `l` / `→` there pick and move on, `Esc` or `h` / `←` close it                           |
| `Enter`                  | confirm the field and move to the next; in an item's reason field, save and approve; in other editors, submit on the last field |
| `Ctrl-S`                 | submit from any field                                                                                                           |
| `Esc`                    | cancel                                                                                                                          |

**Switch Repo**

| key                      | meaning                                                                                    |
| ------------------------ | ------------------------------------------------------------------------------------------ |
| type                     | filter the listed repos; an absolute path opens an install, an `owner/repo` starts it here |
| `Tab` / `Shift-Tab`      | between the text field and the list, keeping the last highlighted repo                     |
| `j` / `k` (or `↑` / `↓`) | move through the list, once one is highlighted; in the text field they are letters         |
| `Enter`                  | open the highlighted repo, or act on what is typed                                         |
| `Esc`                    | back, or quit when no install is open yet                                                  |

On a path with no install, `Enter` shows what installing there would change and writes nothing. On that plan: `Enter` makes exactly those changes, `s` shows the other mode's plan (tracked or solo), `j` / `k` scroll it, `Esc` leaves the repository as it was.

## Notifications

Press `w` on an issue or PR in a list or item view to track its comments. The app checks tracked items at startup and on a normal ledger refresh (`r`). **Notifications** has **Needs attention** for new comments and unviewed saved PR activity or imported actions, and **Past actions** for quiet or viewed tracked items and viewed saved records. The sidebar `(N)` counts tracked items with unread comments. `v` marks a selected alert viewed and moves it to Past actions while keeping it tracked; one `d` dismisses a selected row. Dismissing a tracked item stops its comment checks. Dismissing a saved watch or imported action hides its row locally without deleting the retained evidence. `Enter`, `l` or `→` opens a tracked item with a fresh GitHub read. The notification list itself reads local data only.

The menu also shows retained PR activity and imported action history. Use `j`/`k` or `Tab` to select and `Enter`, `l` or `→` to open a PR's saved activity. `Esc` or `h` goes back. Previous/More cards page within the current menu. Opening Notifications preserves decision drafts. The separate retained PR activity reader does not cover closed issues.

The second screen groups the saved comments or closure explanations for that PR. Each card carries a bounded excerpt. `Enter`, `l` or `→` on a comment or explanation opens the PR item with a fresh GitHub read in the existing item view. `Esc` or `h` returns to the selected card. Select a labeled Previous/More card and press `Enter` to page. The retained card and refreshed PR details can describe different moments. `Ctrl-D`/`Ctrl-U` scrolls, `?` toggles help, and `q` quits with the usual draft confirmation.

Full source text, original and conflicting revisions and watch context remain available through the [offline readers](appeal-evidence.md#needs-attention-bounded-offline-readers). Reading never acknowledges activity or approves a decision.
