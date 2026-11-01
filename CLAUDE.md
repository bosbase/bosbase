# Project rules

These rules apply to every contributor, human or AI.

## English only for documentation and code comments

All documentation and all code comments MUST be written in English.

This covers:

- Code comments in every language of the repository (Go, JavaScript/TypeScript, Svelte, SCSS/CSS, SQL, shell, YAML/TOML...), including doc comments, TODOs and commented-out explanations.
- Documentation: `README.md`, `docs/`, `apidocs/`, any other Markdown file, API descriptions and the MCP tool descriptions/instructions.
- Commit messages and pull request descriptions.

Not covered (non-English text is allowed):

- User-facing strings that are meant to be localized, eg. `ui/src/locales/`, and string data such as language names written in their own script.
- Test fixtures and sample data that intentionally contain non-English content.
- Translations of an English document, named `<name>.<language>.md` (eg. `docs/mcp-guide.zh-CN.md`). The English document is the source of truth and must be updated first.

The rule is enforced by `make check-english` (run by `make lint`), which fails on non-English (CJK) characters in code comments and documentation outside the allowed paths.

When you touch a file that still contains non-English comments, translate them.
