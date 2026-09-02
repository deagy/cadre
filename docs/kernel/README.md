# Agentic SDLC Documentation

This directory contains documentation for the Agentic SDLC repository.

## Contents

- `examples/` - End-to-end workflow examples
- `gate-rationale.md` - Explanation of the ten-gate lifecycle design
- `usage-overview.md` - Command usage and examples

## What the kernel governs

Agentic SDLC provides a ten-gate lifecycle governance system for agentic
software development. It enforces structured development workflows with
approval gates at each phase.

## Ten Gates

1. **G1 - Intent**: Capture the development task
2. **G2 - Requirements**: Define requirements baseline
3. **G3 - Design**: Design the solution
4. **G4 - Implementation**: Implement the solution
5. **G5 - Review**: Code review
6. **G6 - Testing**: Test the implementation
7. **G7 - Integration**: Integrate the changes
8. **G8 - Deployment**: Deploy to target environment
9. **G9 - Validation**: Validate the deployment
10. **G10 - Closure**: Close the development task

## Related Repositories

- [`roster/`](../../roster/) - role catalog, routing, and the `cadre` CLI
- [`plugin/`](../../plugin/) - the installable Claude Code / Codex / Cline distribution

## Version

Kernel version: 0.13.0 (separate from coordinated 0.3.0 release)
