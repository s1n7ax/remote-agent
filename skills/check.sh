#!/usr/bin/env bash
# Validate the skills tree: front matter, names, forbidden references, evals
# and the JSON examples in the skill files.
#
# Usage: skills/check.sh        (exit 0 when everything passes, 1 otherwise)
#
# Needs python3 with PyYAML. Without PyYAML, it re-runs itself through
# `nix shell` with a Python that has it.
set -euo pipefail

SKILLS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! python3 -c 'import yaml' 2>/dev/null; then
  if [[ -z "${SKILLS_CHECK_NIX_SHELL:-}" ]] && command -v nix >/dev/null; then
    export SKILLS_CHECK_NIX_SHELL=1
    # shellcheck disable=SC2016 # a Nix expression, not a shell one
    exec nix shell --impure --expr \
      '(builtins.getFlake "nixpkgs").legacyPackages.${builtins.currentSystem}.python3.withPackages (p: [ p.pyyaml ])' \
      --command bash "$0" "$@"
  fi
  echo "check.sh: needs python3 with PyYAML (or nix to fetch it)" >&2
  exit 1
fi

exec python3 -I - "$SKILLS_DIR" <<'PY'
import json
import os
import re
import sys

import yaml

root = sys.argv[1]
errors = []


def rel(path):
    return os.path.relpath(path, root)


def front_matter(path):
    """Return the YAML front matter of a markdown file as a dict, or None."""
    with open(path, encoding="utf-8") as handle:
        text = handle.read()
    match = re.match(r"---\n(.*?)\n---\n", text, re.S)
    if not match:
        errors.append(f"{rel(path)}: no YAML front matter")
        return None, text
    try:
        data = yaml.safe_load(match.group(1))
    except yaml.YAMLError as error:
        errors.append(f"{rel(path)}: front matter does not parse: {error}")
        return None, text
    if not isinstance(data, dict):
        errors.append(f"{rel(path)}: front matter is not a mapping")
        return None, text
    return data, text


def needs(path, data, keys):
    for key in keys:
        value = data.get(key)
        if not isinstance(value, str) or not value.strip():
            errors.append(f"{rel(path)}: front matter needs a non-empty '{key}'")


skills = sorted(
    name
    for name in os.listdir(root)
    if os.path.isdir(os.path.join(root, name)) and name != "agents"
)

# 1. Skills: front matter with name + description, name matches the directory.
for name in skills:
    path = os.path.join(root, name, "SKILL.md")
    if not os.path.isfile(path):
        errors.append(f"{name}/: no SKILL.md")
        continue
    data, text = front_matter(path)
    if data is None:
        continue
    needs(path, data, ["name", "description"])
    if data.get("name") != name:
        errors.append(f"{rel(path)}: name '{data.get('name')}' does not match directory '{name}'")
    if len(str(data.get("description", ""))) > 1024:
        errors.append(f"{rel(path)}: description is longer than 1024 characters")
    # Relative links to sibling files must exist.
    for target in re.findall(r"\]\((\./[^)#]+)\)", text):
        if not os.path.exists(os.path.join(root, name, target)):
            errors.append(f"{rel(path)}: link to missing file {target}")

# 2. Agents: front matter with name + description, name matches the file.
agents_dir = os.path.join(root, "agents")
tool_names = json.load(open(os.path.join(agents_dir, "claude-tool-names.json")))
for entry in sorted(os.listdir(agents_dir)):
    if not entry.endswith(".md"):
        continue
    path = os.path.join(agents_dir, entry)
    data, _ = front_matter(path)
    if data is None:
        continue
    needs(path, data, ["name", "description"])
    if data.get("name") != entry[:-3]:
        errors.append(f"{rel(path)}: name '{data.get('name')}' does not match file name")
    if "tools" in data:
        for tool in str(data["tools"]).split(","):
            if tool.strip() not in tool_names:
                errors.append(f"{rel(path)}: tool '{tool.strip()}' has no entry in claude-tool-names.json")

# 3. Forbidden references: the old ship scripts, the GitHub CLI, clipboards.
forbidden = [
    (re.compile(r"\btasks\.py\b"), "tasks.py"),
    (re.compile(r"\bpr\.py\b"), "pr.py"),
    (re.compile(r"\bpublish\.py\b"), "publish.py"),
    (re.compile(r"(?<![\w./-])gh(?![\w.-])"), "the gh CLI"),
    (re.compile(r"\b(wl-copy|xclip|xsel|pbcopy)\b"), "a clipboard tool"),
]
this_file = os.path.join(root, "check.sh")
for dirpath, dirnames, filenames in os.walk(root):
    dirnames[:] = [d for d in dirnames if d not in ("results", "__pycache__", ".ruff_cache")]
    for filename in filenames:
        path = os.path.join(dirpath, filename)
        if path == this_file or filename.startswith("LICENSE"):
            continue
        try:
            lines = open(path, encoding="utf-8").read().splitlines()
        except UnicodeDecodeError:
            continue
        for number, line in enumerate(lines, 1):
            for pattern, label in forbidden:
                if pattern.search(line):
                    errors.append(f"{rel(path)}:{number}: references {label}")

# 4. Evals: front matter parses; graders have a type; skill-fired matches.
for name in skills:
    evals = os.path.join(root, name, "evals")
    if not os.path.isdir(evals):
        continue
    skill_data, _ = front_matter(os.path.join(root, name, "SKILL.md"))
    if skill_data and skill_data.get("disable-model-invocation"):
        errors.append(f"{name}/SKILL.md: has evals but disables model invocation, so skill-fired can never pass")
    for dirpath, dirnames, filenames in os.walk(evals):
        dirnames[:] = [d for d in dirnames if d != "results"]
        for filename in filenames:
            path = os.path.join(dirpath, filename)
            if filename.endswith(".json"):
                try:
                    json.load(open(path, encoding="utf-8"))
                except json.JSONDecodeError as error:
                    errors.append(f"{rel(path)}: JSON does not parse: {error}")
            elif filename.endswith((".yaml", ".yml")):
                try:
                    yaml.safe_load(open(path, encoding="utf-8"))
                except yaml.YAMLError as error:
                    errors.append(f"{rel(path)}: YAML does not parse: {error}")
            elif filename.endswith(".md"):
                data, _ = front_matter(path)
                if data is None:
                    continue
                if os.path.basename(dirpath) == "graders":
                    if "type" not in data:
                        errors.append(f"{rel(path)}: grader has no 'type'")
                    if data.get("type") == "tool_used" and data.get("tool") == "Skill" and data.get("input_match") != name:
                        errors.append(f"{rel(path)}: skill-fired matches '{data.get('input_match')}', not '{name}'")
    for case in sorted(os.listdir(evals)):
        case_dir = os.path.join(evals, case)
        if case == "results" or not os.path.isdir(case_dir):
            continue
        if not os.path.isfile(os.path.join(case_dir, "prompt.md")):
            errors.append(f"{rel(case_dir)}: no prompt.md")
        if not os.path.isdir(os.path.join(case_dir, "graders")):
            errors.append(f"{rel(case_dir)}: no graders/")

# 5. JSON examples in the skill files parse; question trees follow the rules.
def check_tree(path, tree):
    seen = set()
    for question in tree.get("questions", []):
        qid = question.get("id")
        where = f"{rel(path)}: question '{qid}'"
        if question.get("kind") not in ("single", "multi", "text", "confirm"):
            errors.append(f"{where}: unknown kind {question.get('kind')!r}")
        options = question.get("options", [])
        if len(options) > 3:
            errors.append(f"{where}: more than 3 options")
        if question.get("kind") in ("single", "multi"):
            if len(options) < 2:
                errors.append(f"{where}: a choice needs at least 2 options")
            elif not options[0].get("recommended") or any(o.get("recommended") for o in options[1:]):
                errors.append(f"{where}: exactly one recommended option, and it goes first")

        def refs(cond):
            if "question" in cond:
                yield cond["question"]
            for key in ("all", "any"):
                for sub in cond.get(key, []):
                    yield from refs(sub)

        for ref in refs(question.get("showIf", {})):
            if ref not in seen:
                errors.append(f"{where}: showIf names '{ref}', which is not an earlier question")
        seen.add(qid)


for name in skills:
    path = os.path.join(root, name, "SKILL.md")
    if not os.path.isfile(path):
        continue
    text = open(path, encoding="utf-8").read()
    for block in re.findall(r"```json\n(.*?)\n```", text, re.S):
        try:
            data = json.loads(block)
        except json.JSONDecodeError as error:
            errors.append(f"{rel(path)}: a ```json example does not parse: {error}")
            continue
        if isinstance(data, dict) and "questions" in data:
            check_tree(path, data)

if errors:
    print("check.sh: FAIL", file=sys.stderr)
    for error in errors:
        print(f"  - {error}", file=sys.stderr)
    sys.exit(1)
print(f"check.sh: OK ({len(skills)} skills, agents, evals and examples checked)")
PY
