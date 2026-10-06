# Polish a set of briefs

**Use when** someone asks to combine several batch or focused maintainer briefs into one master brief, or `bin/next` suggests it. Read [PLAYBOOK.md](PLAYBOOK.md) first. This is a synthesis of existing briefs, not another pass through the whole issue and PR backlog.

**Produces** a short `reports/<owner>/<repo>/<date>-master-brief.md` for the named input set. It does not change the ledger, review decisions, batch coverage, or GitHub state. If that filename already belongs to another input set, use `<date>-master-<subject>-brief.md` rather than replacing it.

## 1. Fix the input set

Use the exact brief paths the person named. If none were named, select a bounded group of two to ten completed batch briefs from `reports/<owner>/<repo>/`, favoring ones not reflected in a recent master brief when that is clear. State which paths you selected before reading. Do not silently include every historical brief or assume that a filename proves its claims are current.

Read each brief in full. Treat its linked issue and PR sources as evidence leads, and its recommendations as proposals. Identify repeated items and themes, contradictory recommendations, completed actions, and decisions whose current state could have changed. Follow the decisive links for the few cases likely to survive into the master brief; check current state through a scoped read when the next step depends on it. If working offline, use the saved evidence and say when a material claim cannot be checked. Do not turn an old brief's confidence into human review or GitHub write approval.

## 2. Write the master brief

Choose the most relevant maintainer decisions from the inputs, with no minimum and usually no more than three to five. Prefer consequence, timeliness, and a clear next step; Item Score describes quality and readiness, not priority. An old unresolved thread needs a current reason for maintainer attention to survive the cut. Merge duplicate discussions into one decision. Resolve conflicting recommendations by reading the relevant evidence or hold the call when the conflict remains. The result may consist entirely of issue follow-ups or may have no new decision. A source brief may contribute nothing to the final selection without being treated as unfinished.

Use the same human-facing shape as [maintainer-brief.md](maintainer-brief.md): a dated title and short scope line, then numbered decisions with direct item links, decisive facts, the strongest material hesitation, and **Next:** one action or hold. Normalize each selected-repository item to [PLAYBOOK.md](PLAYBOOK.md)'s canonical issue or PR link on its first mention, even when input briefs used different labels. Mention a source brief only when it helps a maintainer navigate to more detail. Keep selection mechanics, batch IDs, hashes, and tool state out of the prose. Do not add an audit appendix. A short final sentence may name a cross-cutting pattern or risk if it changes what the maintainer should do.

## 3. Hand over

Save the master brief without committing it. Reply with its path, the input briefs used, and the decisions that survived the synthesis. The contributor who asked reviews and commits the reports. Do not edit the input briefs, ledger, or GitHub from this playbook.
