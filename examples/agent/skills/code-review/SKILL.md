---
name: code-review
description: Performs comprehensive code reviews focusing on bugs, security issues, performance, and best practices. Use when reviewing pull requests, patches, or code changes.
license: MIT
metadata:
  author: vinayprograms
  version: "1.0"
---

# Code Review Instructions

When reviewing code, follow this process:

## 1. Security First

Check for:
- SQL injection, XSS, CSRF vulnerabilities
- Hardcoded secrets or API keys
- Insecure cryptographic practices
- Path traversal vulnerabilities
- Input validation issues

## 2. Logic and Correctness

- Look for off-by-one errors
- Check null/nil handling
- Verify error handling paths
- Check race conditions in concurrent code
- Validate edge cases

## 3. Performance

- Identify N+1 query patterns
- Check for unnecessary allocations
- Look for inefficient algorithms
- Verify caching is used appropriately

## 4. Maintainability

- Check naming conventions
- Verify adequate comments for complex logic
- Look for code duplication
- Verify tests exist for new functionality

## Output Format

Structure your review as:

### 🔴 Critical Issues
Issues that must be fixed before merging.

### 🟡 Suggestions
Improvements that would be nice to have.

### 🟢 Positive Notes
Things done well (encourage good practices).

### Summary
One paragraph summary with overall recommendation.
