#!/usr/bin/env bash
# Capture a private test repository and its triage install before deleting the remote.
set -euo pipefail

usage() {
  printf 'Usage: %s OWNER/REPO /absolute/clone-or-dash /absolute/records/archives ARCHIVE_NAME RUN_ID\n' "$0" >&2
  exit 2
}

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
code_root=$(realpath "$script_dir/..")
records=$(realpath -m "${TRIAGE_EVAL_RECORDS:-$code_root/DOCS/triage-playbook-evals}")

[[ $# == 5 ]] || usage
repo=$1
clone=$2
out_dir=$3
name=$4
run_id=$5

[[ $repo =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || usage
[[ $name =~ ^[A-Za-z0-9_.-]+$ ]] || usage
[[ $run_id =~ ^[a-z0-9]{1,12}$ ]] || usage
[[ $out_dir == /* ]] || usage
[[ -d $records/seed/runs/$run_id ]] || { printf 'Run directory is missing: %s\n' "$run_id" >&2; exit 1; }
has_clone=1
if [[ $clone == - ]]; then
  has_clone=0
else
  [[ $clone == /* ]] || usage
  [[ -d $clone/.git && -f $clone/triage-o-mator/.triage-install.json ]] || { printf 'Expected clone and install are missing\n' >&2; exit 1; }
  [[ $(cat "$clone/triage-o-mator/config/repo") == "$repo" ]] || { printf 'Install repository identity differs\n' >&2; exit 1; }
  [[ $(git -C "$clone" remote get-url origin) == "https://github.com/$repo.git" ]] || { printf 'Clone origin differs\n' >&2; exit 1; }
fi

out_dir=$(realpath -m "$out_dir")
[[ $out_dir == "$records"/* ]] || { printf 'Output must be inside %s\n' "$records" >&2; exit 1; }
mkdir -p "$out_dir"
archive="$out_dir/$name.tar.gz"
[[ ! -e $archive && ! -e $archive.sha256 ]] || { printf 'Archive already exists: %s\n' "$archive" >&2; exit 1; }

stage=$(mktemp -d "$out_dir/.archive-$name.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
mkdir -p "$stage/api/items" "$stage/api/prs"

api_pages() {
  local path=$1 dest=$2 page=1 endpoint count
  mkdir -p "$dest"

  while :; do
    endpoint="repos/$repo/$path"
    if [[ $endpoint == *\?* ]]; then
      endpoint+="&per_page=100&page=$page"
    else
      endpoint+="?per_page=100&page=$page"
    fi

    gh api --method GET "$endpoint" > "$dest/$(printf '%03d' "$page").json"
    count=$(python3 -c 'import json,sys; value=json.load(open(sys.argv[1])); assert isinstance(value,list); print(len(value))' "$dest/$(printf '%03d' "$page").json")
    (( count == 100 )) || break
    (( page += 1 ))
  done
}

printf 'Archiving %s from %s\n' "$repo" "$clone"
git ls-remote --refs "https://github.com/$repo.git" > "$stage/refs-before.txt"
gh api --method GET "repos/$repo" > "$stage/api/repository-before.json"
api_pages 'issues?state=all' "$stage/api/issues"
api_pages 'pulls?state=all' "$stage/api/pulls"
api_pages 'issues/comments' "$stage/api/all-issue-comments"
api_pages 'pulls/comments' "$stage/api/all-pr-review-comments"
api_pages 'labels' "$stage/api/labels"

mapfile -t issue_numbers < <(python3 - "$stage/api/issues" <<'PY'
import glob, json, sys
for path in sorted(glob.glob(sys.argv[1] + '/*.json')):
    for item in json.load(open(path)):
        print(item['number'])
PY
)

mapfile -t pr_numbers < <(python3 - "$stage/api/pulls" <<'PY'
import glob, json, sys
for path in sorted(glob.glob(sys.argv[1] + '/*.json')):
    for item in json.load(open(path)):
        print(item['number'])
PY
)

for number in "${issue_numbers[@]}"; do
  gh api --method GET "repos/$repo/issues/$number" > "$stage/api/items/$number.json"
  api_pages "issues/$number/comments" "$stage/api/items/$number-comments"
  api_pages "issues/$number/events" "$stage/api/items/$number-events"
done

for number in "${pr_numbers[@]}"; do
  gh api --method GET "repos/$repo/pulls/$number" > "$stage/api/prs/$number.json"
  api_pages "pulls/$number/files" "$stage/api/prs/$number-files"
  api_pages "pulls/$number/reviews" "$stage/api/prs/$number-reviews"
  api_pages "pulls/$number/comments" "$stage/api/prs/$number-review-comments"
done

gh api --method GET "repos/$repo" > "$stage/api/repository-after.json"
api_pages 'issues?state=all' "$stage/api/issues-after"
api_pages 'pulls?state=all' "$stage/api/pulls-after"
python3 - "$stage" <<'PY'
import glob, json, sys
from pathlib import Path
stage=Path(sys.argv[1])
def projection(directory):
    rows=[]
    for path in sorted(glob.glob(str(stage/'api'/directory/'*.json'))):
        rows.extend((item['number'],item['state'],item['updated_at']) for item in json.load(open(path)))
    return sorted(rows)
for kind in ('issues','pulls'):
    if projection(kind) != projection(kind+'-after'):
        raise SystemExit(f'{kind} changed while archiving; keep the remote and retry later')
PY

git clone --mirror --quiet "https://github.com/$repo.git" "$stage/repository.git"
git -C "$stage/repository.git" fsck --no-reflogs --connectivity-only --no-progress > /dev/null
git ls-remote --refs "https://github.com/$repo.git" > "$stage/refs-after.txt"
cmp "$stage/refs-before.txt" "$stage/refs-after.txt" || { printf 'Git refs changed while archiving; remote retained\n' >&2; exit 1; }
git -C "$code_root" rev-parse HEAD > "$stage/program-commit.txt"
git -C "$code_root" status --porcelain > "$stage/program-worktree-status.txt"
git -C "$code_root" diff HEAD --binary > "$stage/program-tracked-diff.patch"
git -C "$code_root" bundle create "$stage/triage-o-mator.bundle" --all
git -C "$code_root" bundle verify "$stage/triage-o-mator.bundle" > /dev/null
cp -a "$code_root/prompts" "$stage/program-playbooks"
mkdir -p "$stage/evaluation/seed/runs" "$stage/evaluation/seed"
cp -a "$script_dir/README.md" "$script_dir/plan.md" "$script_dir/run-eval.sh" "$script_dir/archive-run.sh" "$stage/evaluation/"
cp -a "$script_dir/seed/README.md" "$script_dir/seed/fixtures.json" "$script_dir/seed/"*.py "$stage/evaluation/seed/"
for fixture in "$records/seed/$run_id.json" "$script_dir/seed/$run_id.json"; do
  if [[ -f $fixture ]]; then
    cp -a "$fixture" "$stage/evaluation/seed/"
    break
  fi
done
if [[ -f $records/seed/gold.json ]]; then
  cp -a "$records/seed/gold.json" "$stage/evaluation/seed/"
fi
cp -a "$records/seed/runs/$run_id" "$stage/evaluation/seed/runs/"

if (( has_clone )); then
  cp -a "$clone/triage-o-mator" "$stage/triage-o-mator"
  git -C "$clone" rev-parse HEAD > "$stage/local-checkout-head.txt"
  python3 - "$clone/triage-o-mator" "$stage/triage-o-mator" <<'PY'
import hashlib, os, sys
from pathlib import Path
def signature(root):
    result={}
    for path in sorted(Path(root).rglob('*')):
        name=str(path.relative_to(root))
        if path.is_symlink(): result[name]=('link',os.readlink(path))
        elif path.is_file(): result[name]=('file',hashlib.sha256(path.read_bytes()).hexdigest())
    return result
if signature(sys.argv[1]) != signature(sys.argv[2]):
    raise SystemExit('Install changed while archiving; remote retained')
PY
else
  printf 'No local clone or triage-o-mator install was available in this workspace.\n' > "$stage/local-install-unavailable.txt"
fi

# Agent installs live outside the kit and may be gone (a cleared /tmp); a missing one is named in the manifest, never skipped quietly.
python3 - "$records/seed/runs/$run_id/agent-installs.json" "$stage" <<'PY'
import json, shutil, sys
from pathlib import Path
index_path=Path(sys.argv[1]); stage=Path(sys.argv[2])
index=json.loads(index_path.read_text()) if index_path.exists() else {}
result={'captured':[],'missing':[]}
for agent, record in sorted(index.items()):
    root=Path(record['root'])
    parts=[root/'agent-handoff.md', root/'private'/'triage-o-mator', root/'public'/'triage-o-mator']
    if not all(part.exists() for part in parts):
        result['missing'].append(agent)
        print(f'warning: agent {agent} install is missing at {root}; recorded as missing', file=sys.stderr)
        continue
    target=stage/'agent-installs'/agent
    (target/'private').mkdir(parents=True); (target/'public').mkdir()
    shutil.copy2(parts[0], target/'agent-handoff.md')
    shutil.copytree(parts[1], target/'private'/'triage-o-mator', symlinks=True)
    shutil.copytree(parts[2], target/'public'/'triage-o-mator', symlinks=True)
    result['captured'].append(agent)
(stage/'agent-installs.json').write_text(json.dumps(result, indent=2)+'\n')
PY

cat > "$stage/README-restore.txt" <<'EOF'
This archive preserves a bare Git mirror, a bundle of the triage-o-mator
program Git history, live playbook text, any tracked worktree diff, the dated
current-run evaluation files and shared evaluation source, and GitHub REST observations. Each
agent's own private and public install is under agent-installs/ when it still existed. When a
local triage-o-mator install was available, it also includes that install,
its immutable evidence cache and analysis records. Install symlinks point to
the original program checkout; relink from the matching program commit after
restoring. GitHub issue/PR numbers and authorship
cannot be recreated exactly in a newly created remote repository; use the saved
JSON, evidence snapshots, Git mirror, and hashes for offline adjudication.
EOF
if (( ! has_clone )); then
  printf 'This capture has no local triage-o-mator install or evidence cache.\n' >> "$stage/README-restore.txt"
fi

python3 - "$stage" "$repo" "$run_id" <<'PY'
import datetime, hashlib, json, os, sys
from pathlib import Path
stage=Path(sys.argv[1]); repo=sys.argv[2]; run_id=sys.argv[3]
files=[]
for path in sorted(stage.rglob('*')):
    if path.is_symlink():
        files.append({'path':str(path.relative_to(stage)),'symlink':os.readlink(path)})
    elif path.is_file():
        files.append({'path':str(path.relative_to(stage)),'sha256':hashlib.sha256(path.read_bytes()).hexdigest(),'bytes':path.stat().st_size})
issues=list((stage/'api'/'items').glob('[0-9]*.json'))
prs=list((stage/'api'/'prs').glob('[0-9]*.json'))
manifest={'format':1,'repository':repo,'run_id':run_id,'captured_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'issues_and_prs':len(issues),'pull_requests':len(prs),'git_mirror':'repository.git','program_bundle':'triage-o-mator.bundle','program_playbooks':'program-playbooks','evaluation':'evaluation','install':'triage-o-mator' if (stage/'triage-o-mator').exists() else None,'agent_installs':json.loads((stage/'agent-installs.json').read_text()),'files':files}
(stage/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
PY

tar -C "$stage" -czf "$archive.tmp" .
tar -tzf "$archive.tmp" > /dev/null
mv "$archive.tmp" "$archive"
(cd "$out_dir" && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
(cd "$out_dir" && sha256sum -c "$name.tar.gz.sha256")
printf 'Verified archive: %s\n' "$archive"
