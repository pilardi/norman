---
title: "Norman Documentation"
type: source
status: draft
date: 2026-11-05
author: Norman agent (session assessment)
tags: [docs, landing]
workspace: norman
---

# Norman Documentation

Welcome to the Norman documentation. This site is automatically generated from markdown files and the OpenAPI spec.

## API Reference

- [Full API Reference](api-reference.html) — Interactive Scalar-powered reference for all endpoints.

## Design Log Methodology

All design documents follow the [Design-Log Methodology](design-log/README.md), which standardizes structure to: **TL;DR → Background → Problem → Prior Art → Q&A → Design → Implementation Plan → Examples → Trade-offs**.

## Design Documents by Category

### 🏗️ Architecture & Proposals

| Document | Status | Description |
|----------|--------|-------------|
| [Memory Proposal](memory-proposal.md) | `draft` | Memory system design (default axis + session bootstrap) |
| [Config Embedding Proposal](config-embedding-proposal.md) | `draft` | Embedded defaults + help message unification |
| [Provider Config Design](provider-config-design.md) | `draft` | Provider CRUD endpoints (`/api/providers`) |

### ✅ Implemented Designs

| Document | Status | Description |
|----------|--------|-------------|
| [MVP Plan](mvp-plan.md) | `implemented` | End-to-end CLI → Agent Server workflow |
| [Session Workflow Proposal](session-workflow-proposal.md) | `implemented` | Session lifecycle design (WS subscription, UUID identity) |
| [Roger Client Design](roger-client-design.md) | `implemented` | Lightweight notification/health-check binary |
| [OpenShell Interaction](openshell-interaction.md) | `implemented` | Sandbox boundary + tool flow |
| [Workspace Lifecycle Proposal](workspace-lifecycle-proposal.md) | `implemented` | stop/restore/purge states |
| [LLM Integration Plan](llm-integration-plan.md) | `implemented` | Provider-agnostic interfaces (`LLM`, `Embedder`) |

### 📋 Assessments (Ongoing Review)

| Document | Status | Description |
|----------|--------|-------------|
| [Task Management Assessment](task-management-assessment.md) | `draft` | Deferred/scheduled task capabilities |
| [Workspace Definition Assessment](workspace-definition-assessment.md) | `implemented` | Workspace as namespace/container |
| [LLM Tool Access Assessment](llm-tool-access-assessment.md) | `implemented` | Typed tool exposure model |
| [Graphify Skill Install Assessment](graphify-skill-install-assessment.md) | `draft` | Python knowledge-graph skill install |
| [CLI Library Assessment](cli-library-assessment.md) | `implemented` | urfave/v3 migration status |

## Project Links

- [GitHub Repository](https://github.com/pilardi/norman)
