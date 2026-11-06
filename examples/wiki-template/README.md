# Handbook

This repository is a knowledge base. [VEDA](https://github.com/Mesteriis/veda-wiki)
reads it as a wiki source, agents update it through VEDA's MCP tools, and
GitHub Pages publishes it as a static site with the same rendering.

## Sections

- [Guides](guides/) — how to write and organise pages.

## How it is organised

- One topic per directory; each directory has a `README.md` that links to its pages.
- [AGENTS.md](AGENTS.md) holds the rules for agents that edit this repository.
- `veda.yaml` names the wiki; `.github/workflows/pages.yml` publishes it.

> [!NOTE]
> Repository visibility decides who can read it in VEDA. A public GitHub Pages
> site is public whatever the repository visibility, so publish only what may
> be read by anyone.
