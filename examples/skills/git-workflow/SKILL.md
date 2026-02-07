---
name: git-workflow
description: Manage git operations including branching, commits, and pull requests. Use when working with version control or when user mentions git, commits, branches, or PRs.
license: MIT
metadata:
  author: vinayprograms
  version: "1.0"
allowed-tools: Bash(git:*)
---

# Git Workflow Instructions

## Branch Management

When creating branches:
- Use descriptive names: `feature/`, `fix/`, `docs/`, `refactor/`
- Keep names lowercase with hyphens
- Example: `feature/add-user-auth`

## Commit Messages

Follow conventional commits:
```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

Types: feat, fix, docs, style, refactor, test, chore

## Pull Request Workflow

1. Ensure branch is up to date with main
2. Run tests locally before pushing
3. Create PR with descriptive title and body
4. Link related issues
5. Request appropriate reviewers

## Common Commands

```bash
# Create feature branch
git checkout -b feature/my-feature

# Stage and commit
git add -p  # Interactive staging
git commit -m "feat(scope): description"

# Push and create PR
git push -u origin HEAD
```
