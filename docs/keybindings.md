# Keybindings

The TUI's key scheme, agreed 2026-09-19. Every binding is defined once in `tui/keys.go`, which the handlers, the footer and `?` all read, so a change goes there and here together.

Printable keys remain text while editing a text field. Destructive actions and bulk decision changes need the same key twice, except dismissing one row in Notifications with `d` and closing or reopening through the comment composer with `Ctrl-S`; another key cancels the confirmation. Item actions apply to ticked items, or to the item in front when nothing is ticked.

**Mouse:** click a sidebar entry or item tab to open it. Click a card to select it and click it again to open it; right-click a card to tick it where Space does. The wheel scrolls the active view. Click a form field to focus it, click an already focused choice field to open its options, and click an option to choose it. Footer key hints are clickable and run the same actions as their keys, including confirmation steps.

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

**Untriaged list**

| key | meaning                                     |
| --- | ------------------------------------------- |
| `i` | cycle the item kind: issues, PRs, or both   |
| `O` | reverse age order between oldest and newest |

**Item actions**

`f` opens Local dataset. There, `d` downloads or updates the open backlog, `r` resumes, `n` changes the item limit, `u` measures cache size, `y` copies the agent prompt, and `x` stops the current operation. `j`/`k` scrolls the status; `Esc`/`h` closes the menu without stopping. Repository switches and TUI exit cancel owned corpus processes. See [dataset download](evidence.md#download-from-the-tui).

| key       | meaning                                                                                                                                               |
| --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `s`       | save the decision for review; leave the reason field with `Tab` first                                                                                 |
| `S`       | save and approve the decision in the open item or duplicate comparison; leave the reason with `Tab` first; records local human review only            |
| `a`       | approve, its only meaning; on ticked items it always needs a second press                                                                             |
| `m` / `M` | mark as duplicate: on an item or pair, opens the comparison; there, marks the hovered candidate a duplicate of the item on top / makes it that item   |
| `b` / `B` | add to a group (pick one, write a note) / add to the last group; works on hovered and ticked items                                                    |
| `o`       | open on GitHub                                                                                                                                        |
| `u`       | undo one step, with a second press: take back an approval, or clear an unreviewed decision                                                            |
| `c`       | compose a GitHub comment; `Ctrl-P` toggles Markdown preview; `Ctrl-S` approves and publishes                                                          |
| `C`       | compose in `$EDITOR`; save and exit to load the Markdown preview; also works from comment preview                                                     |
| `x` / `X` | compose an explanatory closing comment inline / in `$EDITOR`; `Ctrl-S` approves the text, target and closure together                                 |
| `v` / `V` | reopen closed issues or PRs with a comment inline / in `$EDITOR`; on an item list uses ticked items or the hovered one; bulk `Ctrl-S` reviews targets |

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

Press `w` on an issue or PR in a list or item view to track its comments. The app checks tracked items at startup and on a normal ledger refresh (`r`). **Notifications** shows one card per issue or PR, combining its tracked comments, closure proposal, retained PR activity and imported actions. A card is in **Needs attention** while any of its sources needs review; otherwise it is in **Past actions**. The sidebar `(N)` counts distinct items with unread tracked comments or unviewed closure proposals at startup and after a normal refresh. `v` marks every viewable source on the selected item viewed; it has no effect on an item in Past actions. One `d` dismisses the item's local sources, stopping comment tracking and hiding its proposal or retained rows without deleting their saved records or evidence. If one source changes during a combined action, completed changes remain and the menu reloads to show them. The notification list itself reads local data only.

Pending PR closure proposals appear first in Needs attention. On a card, `Enter`, `l` or `→` opens its proposal when present, then its retained activity or imported actions, then the current item. To open a specific source, press `1` for the proposal, `2` for the current issue or PR, `3` for retained activity, or `4` for imported actions; the footer shows available sources. In a proposal reader, one `a` approves the displayed proposal and starts execution after its exact saved review is checked; `d` dismisses that item's local sources. A completed closure proposal leaves Notifications while its saved proposal record remains available for audit; a separately tracked PR keeps its comment-tracking card. Uncertain closure outcomes stay visible for inspection. Press `w` on an item or its opened PR to track comments; repeating `w` keeps the existing comment baseline. The reader scrolls with `j`/`k` or `Ctrl-D`/`Ctrl-U`. `Enter` or `l` there opens the current PR on its Body tab, and `Esc` or `h` returns to the proposal. To handle several proposals, press `Space` on their cards to tick them, then `a` to open their exact review, or `A` to review every active pending proposal, including viewed ones. Read each proposal and press the same key again to approve the set. `Esc` cancels preparation or review. Execution stops on the first uncertain outcome and retains separate comment and close results. Viewing a proposal does not approve it; dismissing one excludes it from `A`. `q` quits the reader.

The menu also shows retained PR activity and imported action history. Use `j`/`k` or `Tab` to select and `Enter`, `l` or `→` to open a PR's saved activity. `Esc` or `h` goes back. Previous/More cards page within the current menu. Opening Notifications preserves decision drafts. The separate retained PR activity reader does not cover closed issues.

The second screen groups the saved comments or closure explanations for that PR. Each card carries a bounded excerpt. `Enter`, `l` or `→` on a comment or explanation opens the PR item with a fresh GitHub read in the existing item view. `Esc` or `h` returns to the selected card. Select a labeled Previous/More card and press `Enter` to page. The retained card and refreshed PR details can describe different moments. `Ctrl-D`/`Ctrl-U` scrolls, `?` toggles help, and `q` quits with the usual draft confirmation.

Full source text, original and conflicting revisions and watch context remain available through the [offline readers](appeal-evidence.md#needs-attention-bounded-offline-readers). Reading never acknowledges activity or approves a decision.
