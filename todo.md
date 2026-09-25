## Open questions, not scheduled

- [ ] Nothing in the tool produces an external closure record: `cache closure-import` needs a hand-written version-1 claim JSON, a snapshot ID and a timeline event ID looked up by hand. The Notifications list only fills if someone assembles those files. Decide whether that stays a documented manual step or gets a producer
- [x] `bin/_bulk.py:116` runs `git fetch` into the target repository's checkout for PR heads. `AGENTS.md` invariant 3 allows it; `prompts/PLAYBOOK.md` ground rule 8 still forbids an agent from doing exactly that in the same clone. The distinction (the tool may, the agent may not) is defensible but should be written down where both are read
- [ ] The Bjarne pipeline itself — `cache candidates`, `cache discover`, `group create-candidate` — has no TUI surface at all. That may be fine, but it is the part of the prompt with the least visible result
