# Design Log Methodology — Norman Project Rules

This rule provides standards for design log files in this project.

## Overview

As projects grow, AI models often hit a "Context Wall" — making suggestions that conflict with previous decisions or losing track of the codebase's architectural evolution. The **Design-Log Methodology** solves this by maintaining a version-controlled history of intent, decisions, and implementation results.

Instead of writing complex prompts, you maintain a `./docs/design-log/` folder. The AI reads these logs to understand the "why" behind your code before it ever touches a line of production logic.

## Key Principles

- **Read Before You Write**: The AI must check existing logs to understand constraints and reuse existing patterns.
- **Design Before You Implement**: No code is written until a Markdown log is created, reviewed, and approved.
- **The Socratic Method**: The AI is instructed to ask questions within the log to clarify edge cases and logic gaps before implementation.
- **Immutable History**: The "Design" section of a log is frozen once coding begins. All changes, deviations, and results are appended to an "Implementation Results" section, ensuring the history remains honest.
- **Subtract and Reality-Check Before You Present**: Do the hard work in the first draft, not in review. Prefer subtraction — justify why an existing primitive doesn't already suffice before adding new surface — and trace any runtime-touching design through the actual code (with `file:line` citations) rather than reasoning about it abstractly.

## Standard Log Structure

Every design log should follow this structure:

1. **TL;DR** — Settled decisions and non-obvious constraints at the very top
2. **Background** — Context and history leading to the problem
3. **Problem** — The specific gap or constraint being addressed
4. **Prior Art / Adjacent Mechanisms** — Existing primitives, subsystems, and related logs that solve or constrain the problem
5. **Questions & Answers** — Clarifying questions asked *before* implementation; answers appended later
6. **Design** — The chosen solution with rationale (frozen once coding starts)
7. **Implementation Plan** — Phased steps to build it
8. **Examples** — ✅/❌ good/bad patterns, realistic code snippets
9. **Trade-offs** — What was sacrificed for the chosen approach

## When Implementing

- Follow the implementation plan phases from the design log
- Write tests first or update existing tests to match new behavior
- Do not update the initial "Design" section once implementation started
- Append with an "Implementation Results" section as you go
- Document deviations: explain why implementation differs from design
- Include test results (X/Y passing) in implementation notes

## Frontmatter Convention

Every file should include YAML frontmatter:

```yaml
---
title: "Document Title"
type: assessment|proposal|implementation|decision|retrospective
status: draft|reviewed|implemented|deprecated
date: YYYY-MM-DD
author: Name (via tool)
related: [related-file-1.md, related-file-2.md]
---
```

## Status Legend

| Status | Meaning |
|--------|---------|
| `draft` | Under active development; may change |
| `reviewed` | Passed peer review; approved for implementation |
| `implemented` | Code exists matching the design (may have deviations) |
| `deprecated` | Superseded by a newer design or decision |
