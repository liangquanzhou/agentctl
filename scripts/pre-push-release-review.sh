#!/bin/bash
# pre-push hook: release review checklist
# Triggers only when pushing a version tag (v*)
# Mirrors the checks in ~/.config/agentctl/commands/release-review.md

set -e

# Detect if we're pushing a tag
pushing_tag=false
tag_name=""
while read local_ref local_oid remote_ref remote_oid; do
    if [[ "$local_ref" == refs/tags/v* ]]; then
        pushing_tag=true
        tag_name="${local_ref#refs/tags/}"
    fi
done

if [ "$pushing_tag" = false ]; then
    exit 0
fi

echo "═══════════════════════════════════════"
echo "Release Review: agentctl → $tag_name"
echo "═══════════════════════════════════════"

has_fail=false
has_warn=false

# ── 1. Workspace state ──
if [ -n "$(git status --porcelain)" ]; then
    echo "1. 工作区状态    [FAIL]  有未提交的变更"
    git status --short
    has_fail=true
else
    echo "1. 工作区状态    [PASS]"
fi

# ── 2. Tests ──
echo -n "2. 测试          "
if go test ./... -count=1 > /dev/null 2>&1; then
    echo "[PASS]"
else
    echo "[FAIL]  测试未通过"
    has_fail=true
fi

# ── 3. Build ──
echo -n "3. 构建          "
if go build ./... > /dev/null 2>&1; then
    echo "[PASS]"
else
    echo "[FAIL]  编译失败"
    has_fail=true
fi

# ── 4. Docs sync ──
last_tag=$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")
if [ -n "$last_tag" ]; then
    changed_files=$(git diff "$last_tag"..HEAD --name-only)
    code_changed=false
    docs_changed=false

    echo "$changed_files" | grep -qE '^(internal/|cmd/)' && code_changed=true
    echo "$changed_files" | grep -qE '^README' && docs_changed=true

    if [ "$code_changed" = true ] && [ "$docs_changed" = false ]; then
        echo "4. 文档同步      [WARN]  代码改了但 README 没更新"
        echo "   变更的代码文件:"
        echo "$changed_files" | grep -E '^(internal/|cmd/)' | head -10 | sed 's/^/     /'
        has_warn=true
    else
        echo "4. 文档同步      [PASS]"
    fi
else
    echo "4. 文档同步      [PASS]  (首次发布，跳过)"
fi

# ── 5. Version number ──
if [ -n "$last_tag" ]; then
    has_breaking=$(git log "$last_tag"..HEAD --oneline | grep -iE 'feat!:|BREAKING CHANGE' || true)
    if [ -n "$has_breaking" ]; then
        # Check if only patch incremented
        last_minor=$(echo "$last_tag" | sed 's/v\([0-9]*\)\.\([0-9]*\)\..*/\1.\2/')
        new_minor=$(echo "$tag_name" | sed 's/v\([0-9]*\)\.\([0-9]*\)\..*/\1.\2/')
        if [ "$last_minor" = "$new_minor" ]; then
            echo "5. 版本号        [WARN]  有 breaking change 但只递增了 patch"
            echo "$has_breaking" | sed 's/^/     /'
            has_warn=true
        else
            echo "5. 版本号        [PASS]"
        fi
    else
        echo "5. 版本号        [PASS]"
    fi
else
    echo "5. 版本号        [PASS]"
fi

# ── 6. Commit quality ──
if [ -n "$last_tag" ]; then
    bad_commits=$(git log "$last_tag"..HEAD --oneline | grep -vE '^[a-f0-9]+ (feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\(.*\))?!?:' || true)
    if [ -n "$bad_commits" ]; then
        echo "6. Commit 质量   [WARN]  以下 commit 不符合 Conventional Commits:"
        echo "$bad_commits" | sed 's/^/     /'
        has_warn=true
    else
        echo "6. Commit 质量   [PASS]"
    fi
else
    echo "6. Commit 质量   [PASS]"
fi

# ── Conclusion ──
echo "═══════════════════════════════════════"
if [ "$has_fail" = true ]; then
    echo "结论: NO-GO ❌  (有 FAIL 项，push 已阻止)"
    echo "═══════════════════════════════════════"
    exit 1
elif [ "$has_warn" = true ]; then
    echo "结论: GO WITH WARNINGS ⚠️"
    echo "═══════════════════════════════════════"
    read -p "继续 push? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "push 已取消"
        exit 1
    fi
else
    echo "结论: GO ✅"
    echo "═══════════════════════════════════════"
fi
