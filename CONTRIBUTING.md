# Contributing

Thank you for contributing to Bosbase!

## Language

Documentation and code comments must be written in **English** (see the full rule in [CLAUDE.md](CLAUDE.md)):

- code comments in every language of the repository;
- `README.md`, `docs/`, `apidocs/` and all other Markdown files;
- commit messages and pull request descriptions.

Localized UI strings (`ui/src/locales/`), test data and translations named `<name>.<language>.md` are the only exceptions.

Run the check before opening a pull request:

```sh
make check-english
```

## Checks

```sh
make lint   # includes check-english
make test
```
