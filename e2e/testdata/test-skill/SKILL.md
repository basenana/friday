---
name: test-skill
description: A minimal skill used by the friday e2e test suite
allowed_tools: ""
---

# Test Skill

This is a minimal skill definition used by the e2e tests to verify that the
skills system (loader, registry, hook injection, and the `list_skills` /
`load_skill` / `load_skill_resource` tools) works end-to-end with a real LLM.

When loaded, this skill instructs the agent to always finish its response with
the literal phrase `TEST_SKILL_LOADED`. This makes it easy to assert from a
test that the LLM actually read the skill instructions.
