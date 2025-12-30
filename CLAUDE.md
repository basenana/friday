# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Friday is an intelligent QA system using RAG (Retrieval-Augmented Generation) that matches user queries against a
vectorized knowledge base and summarizes responses via LLMs (OpenAI, Gemini, GLM-6B). It includes a multi-agent
framework with tool-calling capabilities.

## Commands

```bash
make build    # Build for darwin/arm64, darwin/amd64, linux/arm64, linux/amd64
make test     # Run all unit tests (go test ./...)
```

## Architecture

### Entry Points

- `cmd/main.go` - Cobra CLI with `serve` (HTTP API) and `agent` commands
- `api/server.go` - Gin-based HTTP server with document/namespace endpoints

### Agent System (`agents/`)

- `react/` - ReAct-style agent with tool loop (max 5 iterations, 20 tool calls)
- `coordinator/` - Orchestrates expert sub-agents, generates summary reports
- `knowledge/` - Vector-based knowledge retrieval
- `research/`, `planning/`, `summarize/` - Specialized agents

### Deprecated Packages (`pkg/`)

Everything defined in the `pkg/` belongs to previous versions
and contains many implementations that can be referenced, but should no longer be used in the current version.

### Storage (`storehouse/`)

- Session management and message storage
- Vector search (Redis, PostgreSQL, PGVector)
- Document stores (MeiliSearch, PostgreSQL)

### Key Types (`types/`)

- `Message` - Conversation messages with tool calls
- `Session` - Chat or Agentic sessions with purpose/summary
- `Document`/`Chunk` - Knowledge base entries with vectors

### Memory System (`memory/`)

- Conversation history with token tracking
- Supports memory forks for sub-agents
- Session hooks: before_model, after_model, before_closed

## Configuration (`config/`)

Configures LLM providers, embedding models, vector stores, document stores, text splitters, and thread pool size.
