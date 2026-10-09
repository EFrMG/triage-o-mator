#!/usr/bin/env bash
# Simple entry points for a playbook evaluation run. Agent judgments remain explicit.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
seed_dir="$here/seed"
# Run records, withheld guides and archives are local; this directory is the shared kit.
records=$(realpath -m "${TRIAGE_EVAL_RECORDS:-$here/../DOCS/triage-playbook-evals}")
export TRIAGE_EVAL_RECORDS=$records
runs="$records/seed/runs"
repo=${TRIAGE_EVAL_REPOSITORY:-$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["repository"])' "$seed_dir/fixtures.json")}
clone=${TRIAGE_EVAL_CLONE_PATH:-/workspace/lazygit-clone}

usage() {
  cat >&2 <<EOF
Usage:
  $0 plan RUN_ID [PREVIOUS_ARCHIVE]
  $0 plan-after-reset RUN_ID PREVIOUS_ARCHIVE
  $0 seed RUN_ID PLAN_SHA256 [PREVIOUS_ARCHIVE]
  $0 test-start RUN_ID
  $0 agent-install RUN_ID AGENT private|public
  $0 archive RUN_ID ARCHIVE_NAME
  $0 verify /absolute/path/to/ARCHIVE.tar.gz
  $0 delete /absolute/path/to/ARCHIVE.tar.gz --confirm-repo $repo

The test-start command verifies the seed and writes freeze.json for the evaluator.
The agent-install command builds one private or public clone and install under
\${TRIAGE_EVAL_AGENT_ROOT:-/tmp}/triage-eval-RUN_ID/AGENT/.
Agents start in their own install with task text and no path into this kit.
EOF
  exit 2
}

[[ $# -ge 1 ]] || usage
subcommand=$1
shift

case "$subcommand" in
  plan)
    [[ $# == 1 || $# == 2 ]] || usage
    run_id=$1
    archive_args=()
    if [[ $# == 2 ]]; then archive_args=(--previous-archive "$2"); fi
    [[ $run_id =~ ^[a-z0-9]{1,12}$ ]] || usage
    mkdir -p "$runs/$run_id"
    temporary=$(mktemp "$runs/$run_id/.preview.XXXXXX")
    trap 'rm -f -- "$temporary"' EXIT
    python3 "$seed_dir/seed.py" --expected-repo "$repo" --run-id "$run_id" "${archive_args[@]}" > "$temporary"
    preview="$runs/$run_id/preview.json"
    if [[ -e $preview ]]; then
      saved_hash=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan_sha256"])' "$preview")
      current_hash=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan_sha256"])' "$temporary")
      [[ $saved_hash == "$current_hash" ]] || { printf 'Existing preview plan changed; inspect it before replacing %s\n' "$preview" >&2; exit 1; }
    else
      mv "$temporary" "$preview"
    fi
    python3 - "$preview" <<'PY'
import json,sys
plan=json.load(open(sys.argv[1]))
print('Preview:',sys.argv[1])
print('Repository:',plan['repository'])
print('Plan SHA-256:',plan['plan_sha256'])
print('Operations:',len(plan['operations']),'items and',len(plan['followup_comments']),'comments')
PY
    ;;
  plan-after-reset)
    [[ $# == 2 ]] || usage
    run_id=$1
    archive=$2
    [[ $run_id =~ ^[a-z0-9]{1,12}$ ]] || usage
    mkdir -p "$runs/$run_id"
    temporary=$(mktemp "$runs/$run_id/.preview.XXXXXX")
    trap 'rm -f -- "$temporary"' EXIT
    python3 "$seed_dir/seed.py" --expected-repo "$repo" --run-id "$run_id" --previous-archive "$archive" --preview-after-reset > "$temporary"
    preview="$runs/$run_id/preview.json"
    if [[ -e $preview ]]; then
      saved_hash=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan_sha256"])' "$preview")
      current_hash=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan_sha256"])' "$temporary")
      [[ $saved_hash == "$current_hash" ]] || { printf 'Prospective preview plan changed; inspect it before replacing %s\n' "$preview" >&2; exit 1; }
    else
      mv "$temporary" "$preview"
    fi
    python3 - "$preview" <<'PY'
import json,sys
plan=json.load(open(sys.argv[1]))
print('Prospective preview:',sys.argv[1])
print('Repository:',plan['repository'])
print('Plan SHA-256:',plan['plan_sha256'])
print('Operations:',len(plan['operations']),'items and',len(plan['followup_comments']),'comments')
PY
    ;;
  seed)
    [[ $# == 2 || $# == 3 ]] || usage
    run_id=$1
    hash=$2
    archive_args=()
    if [[ $# == 3 ]]; then archive_args=(--previous-archive "$3"); fi
    [[ $run_id =~ ^[a-z0-9]{1,12}$ ]] || usage
    preview="$runs/$run_id/preview.json"
    [[ -f $preview ]] || { printf 'Run plan first\n' >&2; exit 1; }
    expected=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan_sha256"])' "$preview")
    [[ $hash == "$expected" ]] || { printf 'Plan hash differs from saved preview\n' >&2; exit 1; }
    python3 -u "$seed_dir/seed.py" --expected-repo "$repo" --run-id "$run_id" "${archive_args[@]}" --apply --plan-sha256 "$hash" 2> "$runs/$run_id/apply.err" | tee "$runs/$run_id/apply.log"
    python3 "$seed_dir/audit.py" --run-id "$run_id" | tee "$runs/$run_id/audit.out"
    ;;
  test-start)
    [[ $# == 1 ]] || usage
    run_id=$1
    [[ $run_id =~ ^[a-z0-9]{1,12}$ ]] || usage
    python3 "$seed_dir/audit.py" --run-id "$run_id"
    python3 "$seed_dir/freeze_run.py" --run-id "$run_id"
    ;;
  agent-install)
    [[ $# == 3 ]] || usage
    run_id=$1
    agent=$2
    lane=$3
    [[ $run_id =~ ^[a-z0-9]{1,12}$ && $agent =~ ^[a-z0-9]{1,12}$ ]] || usage
    [[ $lane == private || $lane == public ]] || usage
    [[ -s "$runs/$run_id/freeze.json" ]] || { printf 'Run test-start first\n' >&2; exit 1; }
    python3 "$seed_dir/agent_install.py" --run-id "$run_id" --agent "$agent" --lane "$lane"
    ;;
  archive)
    [[ $# == 2 ]] || usage
    run_id=$1
    name=$2
    [[ $run_id =~ ^[a-z0-9]{1,12}$ ]] || usage
    [[ -s "$runs/$run_id/freeze.json" && -s "$runs/$run_id/report.md" ]] || { printf 'Run freeze and one report.md are required in the local records at seed/runs/%s/ for a completed-run archive. Use archive-run.sh directly for a partial capture.\n' "$run_id" >&2; exit 1; }
    "$here/archive-run.sh" "$repo" "$clone" "$records/archives" "$name" "$run_id"
    ;;
  verify)
    [[ $# == 1 ]] || usage
    archive=$(realpath "$1")
    [[ -f $archive && -f $archive.sha256 ]] || { printf 'Archive or checksum missing\n' >&2; exit 1; }
    recorded=$(awk '{print $1}' "$archive.sha256")
    actual=$(sha256sum "$archive" | awk '{print $1}')
    [[ $recorded == "$actual" ]] || { printf 'Archive checksum differs\n' >&2; exit 1; }
    tar -tzf "$archive" > /dev/null
    tar -xOf "$archive" ./manifest.json | python3 -c 'import json,sys; m=json.load(sys.stdin); print("Verified:",m["repository"],"run:",m.get("run_id","historical"),"items:",m["issues_and_prs"],"PRs:",m["pull_requests"],"local install:",bool(m["install"]),"agent installs:",m.get("agent_installs","not recorded"))'
    ;;
  delete)
    [[ $# == 3 && $2 == --confirm-repo && $3 == "$repo" ]] || usage
    archive=$(realpath "$1")
    "$0" verify "$archive"
    tar -xOf "$archive" ./manifest.json | python3 -c 'import json,sys; assert json.load(sys.stdin)["repository"] == sys.argv[1], "archive repository differs"' "$repo"
    python3 "$seed_dir/verify_live_archive.py" "$archive" "$repo"
    gh repo delete "$repo" --yes
    printf 'Deleted archived remote: %s\n' "$repo"
    ;;
  *) usage ;;
esac
