package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	result := next.(model)
	result.trackStatus(m)
	result.layout()

	return result, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case commentEditorMsg:
		return m.finishCommentEditor(msg)
	case commentMsg:
		return m.finishComment(msg)
	case notificationsMsg:
		return m.finishNotifications(msg)
	case notificationDoneMsg:
		return m.finishNotificationChange(msg)
	case actionHistoryMsg:
		return m.finishActionHistory(msg)
	case attentionMsg:
		return m.finishAttention(msg)
	case corpusMsg:
		return m.finishCorpus(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true

		return m, nil

	case undoDoneMsg:
		return m.onUndoDone(msg)
	case sidebarCountsMsg:
		return m.onSidebarCounts(msg)
	case exportProgressMsg:
		return m.onExportProgress(msg)
	case groupsLoadedMsg:
		return m.onGroupsLoaded(msg)
	case batchesLoadedMsg:
		return m.onBatchesLoaded(msg)
	case similarLoadedMsg:
		return m.onSimilarLoaded(msg)
	case pairsLoadedMsg:
		return m.onPairsLoaded(msg)
	case notDuplicatesMsg:
		return m.onNotDuplicates(msg)
	case yankedMsg:
		return m.onYanked(msg)
	case installPlannedMsg:
		return m.onInstallPlanned(msg)
	case installedMsg:
		return m.onInstalled(msg)
	case batchAppliedMsg:
		m.batches.busy = false
		if msg.err != nil {
			m.failErr("Couldn't apply the proposals", msg.err)

			return m, nil
		}

		m.status = msg.summary

		return m, reloadLedgerCmd(m.installRoot, m.repo)
	case tea.KeyPressMsg:
		m.lastMouseTarget = ""
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg.Mouse())
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg.Mouse())
	case tea.PasteMsg:
		return m.handlePaste(msg)

	case statusTickMsg:
		return m.onStatusTick()

	case fetchSyncDoneMsg:
		if msg.repo != "" && msg.repo != m.repo {
			return m, nil
		}
		m.refreshing = false
		if msg.err != nil {
			m.failErr("Couldn't fetch from GitHub", msg.err)

			return m, nil
		}
		if msg.trackingErr == nil {
			m.sidebar.notificationCount = msg.unreadTotal
		} else {
			m.recordError("Tracked comment check failed", msg.trackingErr)
		}

		// A confirmation waiting for its second key press keeps the status line; the sync result isn't worth hiding it for.
		if !m.statusPinned() {
			m.status = "Synced. Loading ledger…"
			if msg.trackingErr != nil {
				m.status = "Ledger synced; tracked comment check failed. ! shows details."
			}
		}
		m.detail.cache = make(map[Key]EnrichedItem)
		detailCmd := m.refreshLiveDetail()
		if m.notifications.open {
			next, notificationCmd := m.openNotifications()
			return next, tea.Batch(reloadLedgerCmd(m.installRoot, m.repo), notificationCmd, detailCmd)
		}
		return m, tea.Batch(reloadLedgerCmd(m.installRoot, m.repo), detailCmd)
	case trackDoneMsg:
		return m.finishTracking(msg)

	case ledgerReloadedMsg:
		if msg.repo != m.repo {
			return m, nil
		}

		if msg.err != nil {
			m.failErr("Couldn't read the ledger", msg.err)

			return m, nil
		}

		m.items = msg.items
		m.recomputeSidebarCounts()
		m.refreshActiveList()
		if m.form.saved && !m.form.dirty {
			if it, ok := m.findItem(m.detail.key); ok {
				m.form.LoadItem(it)
			}
		}
		if m.status == "Synced. Loading ledger…" {
			m.status = "Ready."
		}

		// Every save, approval and sync changes what's next.
		return m, nextCmd(m.installRoot, m.repo)

	case nextLoadedMsg:
		if msg.repo == m.repo {
			m.nextSteps, m.nextErr = msg.steps, msg.err
			if msg.err != nil {
				m.recordError("Couldn't work out the next steps", msg.err)
			}

			if m.nextSteps == nil && msg.err == nil {
				m.nextSteps = []nextStep{}
			}
		}

		return m, nil

	case openedMsg:
		if msg.err != nil {
			m.status = "Couldn't open the browser: " + msg.err.Error()
		} else {
			m.status = "Opened " + msg.url + " in the browser."
		}

		return m, nil

	case enrichedMsg:
		if msg.root != "" && (msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.detail.generation || msg.key != m.detail.key) {
			return m, nil
		}
		if m.detail.blockLegacy || m.detail.enriched.Evidence != nil {
			return m, nil
		}

		m.detail.OnEnriched(msg)
		if msg.afterReopen {
			m.status = "Item reopened; comments refreshed."
			if msg.err != nil {
				m.fail("Item reopened, but reloading comments failed. ! shows details.")
			}
		}
		if msg.afterClose {
			m.status = "Comment published and item closed; comments refreshed."
			if msg.err != nil {
				m.fail("Item closed, but reloading comments failed. ! shows details.")
			}
		}
		if msg.afterComment {
			m.status = "Comment published; comments refreshed."
			if msg.err != nil {
				m.fail("Comment published, but reloading comments failed. ! shows details.")
			}
		}
		if msg.err != nil && m.detail.key == msg.key {
			// The item says so where its content would be; ! has the details.
			m.recordError(fmt.Sprintf("Couldn't load %s #%d", msg.key.Kind, msg.key.Number), msg.err)
		}

		// Landed on the Diff tab while the item was still loading: fetch the diff now.
		return m, m.diffIfNeeded()
	case evidenceReadMsg:
		return m.finishEvidenceRead(msg)

	case applyDoneMsg:
		if msg.err != nil {
			what := "Couldn't save the decision"
			if msg.approval {
				what = "Couldn't approve"
			}

			m.failErr(what, msg.err)

			return m, nil
		}

		if !msg.approval || msg.snapshot != nil {
			if draft, ok := m.drafts[msg.key]; ok && (msg.snapshot == nil || draft == *msg.snapshot) {
				delete(m.drafts, msg.key)
			}

			if m.detail.key == msg.key && (msg.snapshot == nil || m.form.Snapshot() == *msg.snapshot) {
				m.form.saved = true
				m.form.dirty = false
				m.form.proposed = false
				m.leaveSavedItem()
			}

			m.status = "Saved."
		}

		if msg.approval {
			m.status = "Approved."
			if msg.snapshot != nil {
				m.status = "Saved and approved."
			}
			if msg.count > 1 {
				m.status = fmt.Sprintf("Approved %d decisions.", msg.count)
			}

			m.clearTicks()
			m.lastStep = msg.approved
			for _, k := range msg.approved {
				if k == m.detail.key && !m.form.dirty && !m.form.proposed {
					// A clean form can follow our own approval on reload; drafts keep the revision they were based on.
					m.form.saved = true
				}
			}
			// Until the ledger reload lands, a u must already see these as approved.
			for i := range m.items {
				for _, k := range msg.approved {
					if m.items[i].Key() == k {
						m.items[i].Reviewed = true
					}
				}
			}
		}

		return m, reloadLedgerCmd(m.installRoot, m.repo)
	}

	return m, nil
}

// handlePaste follows the same modal priority as handleKey, so pasted text only reaches the active editor.
func (m model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.comment.open:
		if m.comment.busy || m.comment.previewing {
			return m, nil
		}
		var cmd tea.Cmd
		m.comment.text, cmd = m.comment.text.Update(msg)
		return m, cmd
	case m.confirmQuit || m.lastError.open || m.notificationPR.open || m.attention.open || m.actionHistory.open || m.notifications.open || m.corpus.open:
		return m, nil
	case m.themePicker.open:
		if !m.themePicker.searching {
			return m, nil
		}
		before := m.themePicker.query.Value()
		var cmd tea.Cmd
		m.themePicker.query, cmd = m.themePicker.query.Update(msg)
		if m.themePicker.query.Value() != before {
			m.themePicker.selected = 0
			m.previewSelectedTheme()
		}
		return m, cmd
	case m.groups.open:
		if m.groups.busy || m.groups.editing == "" || m.onGroupStatusField() {
			return m, nil
		}
		var cmd tea.Cmd
		m.groups.inputs[m.groups.field], cmd = m.groups.inputs[m.groups.field].Update(msg)
		return m, cmd
	case m.batches.open:
		if m.batches.busy || !m.batches.editing || m.batches.field != 0 {
			return m, nil
		}
		var cmd tea.Cmd
		m.batches.size, cmd = m.batches.size.Update(msg)
		return m, cmd
	case m.dups.open:
		return m, nil
	case m.editingRepo:
		if m.installing.path != "" {
			return m, nil
		}
		m.confirmSwitch = false
		m.status = ""
		m.repoPick = -1
		var cmd tea.Cmd
		m.repoInput, cmd = m.repoInput.Update(msg)
		m.repoRecent = matchingRepos(m.repoAll, m.repoInput.Value())
		m.repoLastPick = 0
		return m, cmd
	case m.typingReason():
		if m.confirmSave {
			m.confirmSave = false
			m.status = ""
		}
		before := m.form.reason.Value()
		var cmd tea.Cmd
		m.form.reason, cmd = m.form.reason.Update(msg)
		if m.form.reason.Value() != before {
			m.form.dirty = true
			m.form.saved = false
			m.form.touched = true
		}
		return m, cmd
	case m.searching:
		before := m.searchInput.Value()
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		if m.searchInput.Value() != before {
			m.showList()
			m.list.Select(0)
		}
		return m, cmd
	default:
		return m, nil
	}
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Modal states intercept every key, in priority order, before any global keybinding; otherwise, typing "r" (or "q", "a", "h", "?", ...) into a text field would trigger refresh, quit, approve, back, etc. instead of being typed. ForceQuit is the one exception: it must always work.
	if key.Matches(msg, keys.ForceQuit) {
		m.notificationsLifecycle.stop()
		m.actionHistoryLifecycle.stop()
		m.attentionLifecycle.stop()
		return m, tea.Quit
	}

	if m.comment.open {
		return m.handleCommentKey(msg)
	}

	if m.confirmQuit {
		return m.handleQuitConfirmKey(msg)
	}

	if !key.Matches(msg, keys.Undo) {
		m.lastStep = nil
	}

	if m.lastError.open {
		return m.handleErrorKey(msg)
	}
	if m.notificationPR.open {
		return m.handleNotificationPRKey(msg)
	}

	if m.attention.open {
		return m.handleAttentionKey(msg)
	}
	if m.actionHistory.open {
		return m.handleActionHistoryKey(msg)
	}
	if m.notifications.open {
		return m.handleNotificationsKey(msg)
	}
	if m.corpus.open {
		return m.handleCorpusKey(msg)
	}

	if (key.Matches(msg, keys.Yank) || key.Matches(msg, keys.YankAll)) && !m.typingText() && !m.themePicker.open && !m.editingRepo {
		text, what := m.yankText(key.Matches(msg, keys.YankAll))
		m.status = "Taking " + what + "…"

		return m, yankCmd(m.installRoot, m.repo, what, text)
	}

	if key.Matches(msg, keys.ErrorDetails) && !m.typingText() && !m.themePicker.open {
		if m.lastError.text == "" {
			m.status = "No errors so far."
		} else {
			m.lastError.open, m.lastError.offset = true, 0
		}

		return m, nil
	}

	if m.themePicker.open {
		return m.handleThemeKey(msg)
	}

	if m.groups.open {
		return m.handleGroupKey(msg)
	}

	if m.batches.open {
		return m.handleBatchKey(msg)
	}

	if m.dups.open {
		return m.handleDupKey(msg)
	}

	if m.confirmSave && !key.Matches(msg, keys.Save, keys.SaveApprove) && !m.typingReason() {
		m.confirmSave = false
		m.status = ""
	}

	if m.confirmApprove && (!key.Matches(msg, keys.Approve) || m.typingReason()) {
		m.confirmApprove = false
		m.status = ""
	}

	if m.batches.confirm != "" && !key.Matches(msg, keys.ApplyAll) {
		m.batches.confirm = ""
		m.status = ""
	}

	if m.listConfirm != "" && msg.String() != m.listConfirm {
		m.listConfirm = ""
		m.status = ""
	}

	if m.editingRepo {
		return m.handleRepoInputKey(msg)
	}

	if m.typingReason() {
		return m.handleReasonKey(msg)
	}

	if m.searching {
		return m.handleSearchKey(msg)
	}

	// An open choice list takes every key (Esc closes it) before anything global, except q: it isn't typing, so it quits as everywhere else.
	if m.focus == FocusDetail && m.form.pick.open {
		if key.Matches(msg, keys.Quit) {
			return m.requestQuit()
		}

		return m.handleDetailKey(msg)
	}

	// In a searched list, going back clears the search first.
	if key.Matches(msg, keys.Back) && m.focus == FocusList && m.searchQuery() != "" {
		m.clearSearch()

		return m, nil
	}

	// In an item, l / → go from the content to the form and h / ← come back (the reason field is typing, so it's reached with Tab). h / ← on the content, and Esc anywhere, still go back.
	if m.focus == FocusDetail && !m.detail.AnySectionFull() {
		switch {
		case m.form.focused == fieldContent && key.Matches(msg, keys.Forward):
			m.form.FocusField(fieldCategory)

			return m, nil
		case m.form.focused != fieldContent && msg.Code != tea.KeyEsc && key.Matches(msg, keys.Back):
			m.form.FocusField(fieldContent)

			return m, nil
		}
	}

	if key.Matches(msg, keys.Back) {
		m.goBack()

		return m, nil
	}

	switch {
	case key.Matches(msg, keys.Quit):
		return m.requestQuit()
	case key.Matches(msg, keys.Help):
		m.showHelp = !m.showHelp

		return m, nil
	case key.Matches(msg, keys.Theme):
		m.openThemePicker()

		return m, nil
	case key.Matches(msg, keys.Refresh), key.Matches(msg, keys.RefreshFull):
		return m.startRefresh(key.Matches(msg, keys.RefreshFull))
	case key.Matches(msg, keys.Corpus):
		if m.noInstall() {
			return m, nil
		}
		m.corpus.open = true
		if !m.corpus.busy {
			m.corpus.operation++
			if m.corpusObserverLifecycle == nil {
				m.corpusObserverLifecycle = &readLifecycle{}
			}
			m.corpusObserverLifecycle.stop()
			m.corpusObserverLifecycle.current = &readProcess{}
			return m, corpusCommand(m.installRoot, m.repo, m.corpusEpoch, m.corpus, "restore", m.corpusObserverLifecycle.current)
		}
		return m, nil
	case key.Matches(msg, keys.Group):
		return m.openGroups()
	case key.Matches(msg, keys.Search) && m.focus == FocusList && m.listReady:
		m.startSearch()

		return m, nil
	}

	switch m.focus {
	case FocusSidebar:
		return m.handleSidebarKey(msg)
	case FocusList:
		return m.handleListKey(msg)
	case FocusDetail:
		return m.handleDetailKey(msg)
	}

	return m, nil
}

// typingReason reports whether keys are going into the decision's reason field, where every printable key is text.
func (m model) typingReason() bool {
	return m.focus == FocusDetail && m.form.focused == fieldReason && !m.detail.AnySectionFull()
}

// startRefresh fetches from GitHub and syncs, unless a fetch is already running. r means this on every screen.
func (m model) startRefresh(full bool) (tea.Model, tea.Cmd) {
	if m.corpus.busy {
		m.status = "Wait for or cancel the corpus operation before refreshing."
		return m, nil
	}
	if m.refreshing {
		return m, nil
	}

	m.refreshing = true
	m.status = fetchStatus
	if full {
		m.status = fullFetchStatus
	}

	m.refreshStatus = m.status

	return m, fetchSyncCmd(m.installRoot, m.repo, full)
}

// requestQuit quits immediately if nothing would be lost, otherwise asks for a second explicit quit before discarding in-memory drafts.
func (m model) requestQuit() (tea.Model, tea.Cmd) {
	m.commitDraftIfDirty()
	if len(m.drafts) == 0 {
		return m, tea.Quit
	}

	m.confirmQuit = true
	m.status = fmt.Sprintf("%d unsaved decision(s) this session: quitting will discard them!", len(m.drafts))

	return m, nil
}

func (m model) handleQuitConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Quit) {
		return m, tea.Quit
	}

	// ? shows what the keys do without giving up the pending quit.
	if key.Matches(msg, keys.Help) {
		m.showHelp = !m.showHelp

		return m, nil
	}

	m.confirmQuit = false
	m.status = "Quit cancelled: unsaved decisions are still here."

	return m, nil
}

func (m model) handleRepoInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.moveRepoPick(msg) {
		m.confirmSwitch = false

		return m, nil
	}

	if !key.Matches(msg, keys.Enter) {
		// Anything else means the value or the plan changed: a pending discard confirmation, or a complaint about the previous value, no longer applies.
		m.confirmSwitch = false
		m.status = ""
	}

	if m.installing.path != "" {
		return m.handleInstallPlanKey(msg)
	}

	switch {
	case key.Matches(msg, keys.Cancel):
		if m.noInstall() {
			return m, tea.Quit // there is nothing behind this screen to go back to
		}

		m.editingRepo = false
		m.repoInput.Blur()

		return m, nil
	case key.Matches(msg, keys.Enter) && m.typedInstallTarget() != "":
		path := m.typedInstallTarget()
		m.installing = installUI{path: path, busy: true}
		m.status = "Working out what installing into " + path + " would change…"

		return m, installPlanCmd(m.installerRoot(), path, false)

	case key.Matches(msg, keys.Enter):
		target, problem := m.repoTarget()
		if problem != "" {
			m.confirmSwitch = false
			m.fail(problem)

			return m, nil
		}

		if target.root == m.installRoot && target.name == m.repo {
			m.editingRepo, m.confirmSwitch = false, false
			m.repoInput.Blur()

			return m, nil
		}

		if reason := m.switchBusy(); reason != "" {
			m.fail("Can't switch yet: " + reason)

			return m, nil
		}

		m.commitDraftIfDirty()
		if len(m.drafts) > 0 && !m.confirmSwitch {
			m.confirmSwitch = true
			m.status = fmt.Sprintf("%d unsaved decision(s) will be discarded. Enter again to switch anyway, Esc to go back and save them.", len(m.drafts))

			return m, nil
		}

		m.confirmSwitch = false
		if err := WriteRepo(target.root, target.name); err != nil {
			m.failErr("Couldn't switch the repo", err)

			return m, nil
		}

		m.editingRepo = false
		m.repoInput.Blur()

		if target.root != m.installRoot {
			return m, m.switchInstall(target.root, target.name)
		}

		return m, m.switchRepo(target.name)
	}

	// Typing means the list is a filter over every install's repos, not the highlighted entry.
	m.repoPick = -1
	var cmd tea.Cmd
	m.repoInput, cmd = m.repoInput.Update(msg)
	m.repoRecent = matchingRepos(m.repoAll, m.repoInput.Value())
	m.repoLastPick = 0

	return m, cmd
}

// onYanked says where the context went, since a clipboard gives no sign of its own.
func (m model) onYanked(msg yankedMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		m.failErr("Couldn't take "+msg.what, msg.err)
	case msg.file != "":
		m.status = fmt.Sprintf("No clipboard here, so %s (%d lines) is in %s", msg.what, msg.lines, msg.file)
	default:
		m.status = fmt.Sprintf("Copied %s (%d lines). Paste it to your agent.", msg.what, msg.lines)
	}

	return m, nil
}

// typedInstallTarget is an absolute path in the picker's field with no install in it or beside it: the one case where Switch Repo offers to make one, instead of only opening what exists.
func (m model) typedInstallTarget() string {
	value := strings.TrimSpace(m.repoInput.Value())
	if m.repoPick >= 0 || !strings.HasPrefix(value, "/") {
		return ""
	}

	root := filepath.Clean(value)
	if isInstall(root) || isInstall(filepath.Join(root, InstallDirName)) {
		return ""
	}

	return root
}

// handleInstallPlanKey drives the plan screen: Enter accepts the plan as shown, s asks for the other mode's plan, and anything that goes back leaves the repository untouched.
func (m model) handleInstallPlanKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.installing.busy {
		return m, nil // a plan or an install is running; let it finish rather than queueing another
	}

	switch {
	case key.Matches(msg, keys.Cancel):
		m.installing = installUI{}
		m.status = "Nothing was installed."

		return m, nil

	case key.Matches(msg, keys.Enter):
		m.installing.busy = true
		m.status = "Installing into " + m.installing.path + "…"

		return m, installCmd(m.installerRoot(), m.installing.path, m.installing.solo)

	case key.Matches(msg, keys.Down):
		m.installing.offset++

		return m, nil

	case key.Matches(msg, keys.Up):
		m.installing.offset = maxInt(m.installing.offset-1, 0)

		return m, nil

	case msg.Text == "s":
		solo := !m.installing.solo
		m.installing = installUI{path: m.installing.path, solo: solo, busy: true}

		return m, installPlanCmd(m.installerRoot(), m.installing.path, solo)
	}

	return m, nil
}

// onInstallPlanned shows what the script said it would change, or why it can't.
func (m model) onInstallPlanned(msg installPlannedMsg) (tea.Model, tea.Cmd) {
	m.installing.busy = false
	if msg.err != nil {
		m.installing = installUI{}
		m.failErr("Couldn't work out what installing there would change", msg.err)

		return m, nil
	}

	m.installing = installUI{path: msg.path, solo: msg.solo, plan: msg.plan}
	m.status = "Nothing written yet."

	return m, nil
}

// onInstalled opens what was just created, so the session lands in it the way picking an existing install does.
func (m model) onInstalled(msg installedMsg) (tea.Model, tea.Cmd) {
	m.installing = installUI{}
	if msg.err != nil {
		m.failErr("Couldn't install there", msg.err)

		return m, nil
	}

	root := filepath.Join(msg.path, InstallDirName)
	if !isInstall(root) {
		root = msg.path
	}

	repo, err := ReadRepo(root)
	if err != nil {
		m.failErr("Installed, but couldn't read what it triages", err)

		return m, nil
	}

	m.editingRepo = false
	m.repoInput.Blur()

	return m, m.switchInstall(root, repo)
}

// repoTarget is what Enter in the picker means: the highlighted entry, an absolute path to another install (or to the repository holding one), an owner/repo to start in this install, or the one entry a typed filter leaves.
// The second return value is what to tell the user when it means none of those.
func (m model) repoTarget() (repoInfo, string) {
	if m.repoPick >= 0 && m.repoPick < len(m.repoRecent) {
		return m.repoRecent[m.repoPick], ""
	}

	value := strings.TrimSpace(m.repoInput.Value())
	if strings.HasPrefix(value, "/") {
		root := filepath.Clean(value)
		if !isInstall(root) {
			if child := filepath.Join(root, InstallDirName); isInstall(child) {
				root = child
			} else {
				return repoInfo{}, fmt.Sprintf("No triage-o-mator install in %s. Run bin/install-to %s from the triage-o-mator checkout first.", root, root)
			}
		}

		repo, err := ReadRepo(root)
		if err != nil {
			return repoInfo{}, fmt.Sprintf("%s is an install, but its config/repo doesn't name a repo to triage.", root)
		}

		return repoInfo{name: repo, root: root}, ""
	}

	if validRepo(value) {
		if m.noInstall() {
			return repoInfo{}, fmt.Sprintf("No install is open, so there is nowhere to start %s. Pick one below, or type the path of a repository that has one.", value)
		}

		return repoInfo{name: value, root: m.installRoot}, ""
	}

	if matches := matchingRepos(m.repoAll, value); value != "" && len(matches) == 1 {
		return matches[0], ""
	}

	return repoInfo{}, fmt.Sprintf("%q is neither a repo nor an install: type owner/repo, an absolute path to another install, or a filter that matches one of the listed repos.", value)
}

func (m model) handleSidebarKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Up):
		m.overview = false
		m.sidebar.Prev()
	case key.Matches(msg, keys.Down):
		m.overview = false
		m.sidebar.Next()
	case key.Matches(msg, keys.Top):
		m.overview = false
		m.sidebar.selected = 0
	case key.Matches(msg, keys.Bottom):
		m.overview = false
		m.sidebar.selected = rowCount() - 1
	case key.Matches(msg, keys.Enter), key.Matches(msg, keys.Forward):
		cmd := m.enterSidebarSelection()

		return m, cmd
	}

	return m, nil
}

func (m model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.activeTab == untriagedTab && m.activeBatch == "" && !m.activePairs && key.Matches(msg, keys.UntriagedKind):
		m.cycleUntriagedKind()
	case m.activeTab == untriagedTab && m.activeBatch == "" && !m.activePairs && key.Matches(msg, keys.UntriagedOrder):
		m.toggleUntriagedOrder()
	case key.Matches(msg, keys.Up):
		m.list.CursorUp()
	case key.Matches(msg, keys.Down):
		m.list.CursorDown()
	case key.Matches(msg, keys.Top):
		m.list.Select(0)
	case key.Matches(msg, keys.Bottom):
		m.list.Select(maxInt(len(m.list.Items())-1, 0))
	case key.Matches(msg, keys.HalfDown):
		for i := 0; i < maxInt(m.list.Paginator.PerPage/2, 1); i++ {
			m.list.CursorDown()
		}
	case key.Matches(msg, keys.HalfUp):
		for i := 0; i < maxInt(m.list.Paginator.PerPage/2, 1); i++ {
			m.list.CursorUp()
		}
	case key.Matches(msg, keys.Enter), key.Matches(msg, keys.Forward):
		cmd := m.selectCurrentListItem()

		return m, cmd
	case key.Matches(msg, keys.ApplyAll):
		// Inside an open batch, A applies its proposals, with the items in view.
		if b := m.batchByID(m.activeBatch); b != nil {
			return m.requestApplyBatch(*b)
		}
	case m.activePairs && key.Matches(msg, keys.Delete):
		return m.requestRuleOutPair()
	case m.activePairs && key.Matches(msg, keys.DeleteAll):
		return m.requestClearHandledPairs()
	case m.activePairs && key.Matches(msg, keys.MarkDup):
		// A hovered pair: m opens its comparison, where m marks, as it does on an item.
		if pair, ok := m.list.SelectedItem().(pairListItem); ok {
			return m, m.openPair(pair)
		}
	case !m.itemListActive():
		// A list of duplicate pairs: the other item actions below need items.
	case key.Matches(msg, keys.Tick):
		m.toggleTick()
	case key.Matches(msg, keys.Approve):
		return m.requestListApprove()
	case key.Matches(msg, keys.Reopen):
		return m.openReopen(m.listTargets())
	case key.Matches(msg, keys.ReopenEditor):
		return m.openExternalReopen(m.listTargets())
	case key.Matches(msg, keys.Undo):
		return m.requestUndo(m.undoTargets())
	case key.Matches(msg, keys.QuickGroup):
		return m.quickAddLastGroup(m.listTargetKeys())
	case key.Matches(msg, keys.MarkDup):
		if li, ok := m.list.SelectedItem().(listItem); ok {
			return m.openDuplicatesFor(li.Key(), true)
		}
	case key.Matches(msg, keys.Track):
		if li, ok := m.list.SelectedItem().(listItem); ok {
			return m.startTracking(li.Key())
		}
	case key.Matches(msg, keys.Delete):
		if m.activeBatch != "" {
			return m.requestBatchRemove()
		}

		m.status = "d removes items from a batch or a group; there's nothing to delete in this view."
	}

	return m, nil
}

// handleReasonKey handles keys while typing the reason: printable keys are text, so only field movement, save, and Esc act.
func (m model) handleReasonKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Letter shortcuts are text here. Enter saves and approves the completed decision; Ctrl-S remains an optional save-for-review alias.
	save := msg.String() == "ctrl+s" || key.Matches(msg, keys.Confirm)
	if m.confirmSave && !save {
		m.confirmSave = false
		m.status = ""
	}

	switch {
	case key.Matches(msg, keys.Cancel):
		m.goBack()

		return m, nil
	case key.Matches(msg, keys.FieldNext):
		m.form.NextField()

		return m, nil
	case key.Matches(msg, keys.FieldPrev):
		m.form.PrevField()

		return m, nil
	case key.Matches(msg, keys.Confirm):
		return m.requestDecisionSave(true)
	case msg.String() == "ctrl+s":
		return m.requestSave()
	}

	before := m.form.reason.Value()
	var cmd tea.Cmd
	m.form.reason, cmd = m.form.reason.Update(msg)
	if m.form.reason.Value() != before {
		m.form.dirty = true
		m.form.saved = false
		m.form.touched = true
	}

	return m, cmd
}

// handleDetailKey handles an open item. On the content (the default focus), j/k and the arrows scroll the active tab; on a choice field, j/k and ↑/↓ change the value, J / K move to the next / previous field (into the reason too), l / → open the list of values, and Enter moves on to the next field.
func (m model) handleDetailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	onChoice := formFieldIsEnum(m.form.focused) && !m.detail.AnySectionFull()
	if onChoice && m.form.pick.open {
		m.form.PickKey(msg)

		return m, nil
	}

	switch {
	case key.Matches(msg, keys.FieldNext):
		if !m.detail.AnySectionFull() {
			m.form.NextField()
		}
	case key.Matches(msg, keys.FieldPrev):
		if !m.detail.AnySectionFull() {
			m.form.PrevField()
		}
	case onChoice && key.Matches(msg, keys.ChoiceNext):
		m.form.NextField()
	case onChoice && key.Matches(msg, keys.ChoicePrev):
		m.form.PrevField()
	case onChoice && key.Matches(msg, keys.OpenList):
		m.form.OpenPick()
	case onChoice && key.Matches(msg, keys.Confirm):
		m.form.NextField()
	case onChoice && key.Matches(msg, keys.ValueNext):
		m.form.CycleValue(1)
	case onChoice && key.Matches(msg, keys.ValuePrev):
		m.form.CycleValue(-1)
	case key.Matches(msg, keys.TabNext):
		m.detail.CycleSection(1)

		return m, m.diffIfNeeded()
	case key.Matches(msg, keys.TabPrev):
		m.detail.CycleSection(-1)

		return m, m.diffIfNeeded()
	case key.Matches(msg, keys.TabJump):
		tab := int(msg.String()[0] - '1')
		if tab >= len(m.detail.sections) {
			return m, nil
		}

		if tab == m.detail.active && !m.detail.AnySectionFull() {
			if m.form.focused == fieldContent {
				m.form.FocusField(fieldCategory)
			} else {
				m.form.FocusField(fieldContent)
			}
		}
		m.detail.JumpSection(tab)

		return m, m.diffIfNeeded()
	case key.Matches(msg, keys.Enter):
		if m.detail.ToggleActiveSection() {
			return m, m.enrichDetailCmd(true)
		}
	case key.Matches(msg, keys.Down):
		m.detail.LineDown(1)
	case key.Matches(msg, keys.Up):
		m.detail.LineUp(1)
	case key.Matches(msg, keys.Top):
		m.detail.GotoTop()
	case key.Matches(msg, keys.Bottom):
		m.detail.GotoBottom()
	case key.Matches(msg, keys.HalfDown):
		m.detail.HalfPageDown()
	case key.Matches(msg, keys.HalfUp):
		m.detail.HalfPageUp()
	case key.Matches(msg, keys.Save):
		return m.requestSave()
	case key.Matches(msg, keys.SaveApprove):
		return m.requestDecisionSave(true)
	case key.Matches(msg, keys.Approve):
		return m.requestApprove()
	case key.Matches(msg, keys.Undo):
		if it, ok := m.findItem(m.detail.key); ok {
			return m.requestUndo([]Item{it})
		}
	case key.Matches(msg, keys.MarkDup):
		return m.openDuplicates()
	case key.Matches(msg, keys.Track):
		return m.startTracking(m.detail.key)
	case key.Matches(msg, keys.QuickGroup):
		return m.quickAddLastGroup([]Key{m.detail.key})
	case key.Matches(msg, keys.Open):
		if it, ok := m.findItem(m.detail.key); ok {
			m.status = "Opening " + it.URL + "…"

			return m, openURLCmd(it.URL)
		}
	case key.Matches(msg, keys.CommentEditor):
		return m.openExternalComment()
	case key.Matches(msg, keys.Comment):
		return m.openComment()
	case key.Matches(msg, keys.Close):
		return m.openClose()
	case key.Matches(msg, keys.CloseEditor):
		return m.openExternalClose()
	case key.Matches(msg, keys.Reopen):
		if it, ok := m.findItem(m.detail.key); ok {
			return m.openReopen([]Item{it})
		}
	case key.Matches(msg, keys.ReopenEditor):
		if it, ok := m.findItem(m.detail.key); ok {
			return m.openExternalReopen([]Item{it})
		}
	}

	return m, nil
}

// diffIfNeeded fetches the PR diff the first time its tab is shown.
func (m model) diffIfNeeded() tea.Cmd {
	if m.detail.NeedsActiveDiff() {
		return m.enrichDetailCmd(true)
	}

	return nil
}

func formFieldIsEnum(f formField) bool {
	return f == fieldCategory || f == fieldAction || f == fieldConfidence
}

// requestSave saves the decision, but first warns (and requires a repeated save action) when the fields are still the untouched defaults or the reason is empty, so a stray keypress can't record "bug / label-only / low" with no justification.
func (m model) requestSave() (tea.Model, tea.Cmd) {
	return m.requestDecisionSave(false)
}

func (m model) requestDecisionSave(approve bool) (tea.Model, tea.Cmd) {
	if approve && strings.TrimSpace(m.reviewer) == "" {
		m.fail("Can't approve without a reviewer name. Set git config user.name and reopen the TUI.")

		return m, nil
	}

	if bad := m.form.InvalidValues(); bad != "" {
		m.fail("Can't save " + bad + ": not in config/taxonomy.json. Pick a value first.")

		return m, nil
	}

	var problems []string
	if !m.form.touched {
		problems = append(problems, "defaults unchanged")
	}

	if strings.TrimSpace(m.form.Reason()) == "" {
		problems = append(problems, "no reason")
	}

	if len(problems) > 0 && (!m.confirmSave || m.confirmSaveApproval != approve) {
		m.confirmSave = true
		m.confirmSaveApproval = approve
		msg := strings.Join(problems, ", ")
		action := "s again to save anyway."
		if m.typingReason() {
			action = "Ctrl-S again to save for review anyway."
		}
		if approve {
			action = "S again to save and approve anyway."
			if m.typingReason() {
				action = "Enter again to save and approve anyway."
			}
		}

		m.status = strings.ToUpper(msg[:1]) + msg[1:] + ": " + action

		return m, nil
	}

	m.confirmSave = false

	return m, m.saveDecisionCmd(approve)
}

// requestApprove approves the saved decision. bin/apply --approve ignores the form, so unsaved edits need a second press to make clear they won't be part of what gets approved.
func (m model) requestApprove() (tea.Model, tea.Cmd) {
	it, ok := m.findItem(m.detail.key)
	if !ok || it.Untriaged() {
		m.status = "Nothing to approve yet: s saves for review; S saves and approves."

		return m, nil
	}

	if m.form.dirty && !m.confirmApprove {
		m.confirmApprove = true
		m.status = fmt.Sprintf("Unsaved edits won't be approved. Press a again to approve the saved %s/%s, or S to save and approve your edits.", it.Category, it.Action)

		return m, nil
	}

	m.confirmApprove = false

	return m, approveCmd(m.installRoot, m.detail.key, m.reviewer)
}

func (m model) saveDecisionCmd(approve bool) tea.Cmd {
	snapshot := m.form.Snapshot()
	by, reviewedBy := m.reviewer, ""
	if approve {
		reviewedBy = m.reviewer
		if m.form.proposed && snapshot == m.form.proposalSnapshot {
			by = m.form.proposalBy
			if by == "" {
				by = "agent"
			}
		}
	}

	cmd := applyDecisionCmd(m.installRoot, m.detail.key, m.form.Category(), m.form.Action(), m.form.Confidence(), m.form.Reason(), m.form.proposalNotes, by, m.activeBatch, reviewedBy)

	return func() tea.Msg { msg := cmd().(applyDoneMsg); msg.snapshot = &snapshot; return msg }
}

// leaveSavedItem goes back from the item just saved to where it was opened from; in a list, the cursor moves on to the next item. The ledger reload that follows keeps the cursor on that item (setListEntries), even if the saved one leaves the list.
func (m *model) leaveSavedItem() {
	// Saved from a screen standing on top of the item (the comparison, a group): stay on it.
	if m.dups.open || m.groups.open {
		return
	}

	if m.focus != FocusDetail {
		return
	}

	fromList := !m.dups.returnToDups && !m.groups.returnToGroup && m.listReady
	m.goBack()
	if !fromList {
		return
	}

	if li, ok := m.list.SelectedItem().(listItem); ok && li.Key() == m.detail.key {
		m.list.CursorDown()
	}
}

func (m *model) goBack() {
	switch m.focus {
	case FocusDetail:
		m.commitDraftIfDirty()
		if m.dups.returnToDups {
			m.dups.open = true
			m.dups.returnToDups = false

			return
		}

		if m.groups.returnToGroup {
			m.groups.open = true
			m.groups.returnToGroup = false

			return
		}

		if !m.listReady {
			m.focus = FocusSidebar

			return
		}

		m.focus = FocusList
		// A draft kept just now shows as unsaved in the list.
		m.showList()
	case FocusList:
		m.focus = FocusSidebar
	case FocusSidebar:
		m.listReady = false
		m.overview = true
	}
}
