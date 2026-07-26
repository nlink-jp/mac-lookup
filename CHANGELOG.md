# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.1.0] - unreleased

### Added

- Phase 1 (Planning): RFP in `docs/{en,ja}/`, covering the problem statement,
  command surface, resolution order, exit-code contract, series placement and
  the measured IEEE constraints.
- Phase 2 (Scaffolding): repository structure, Makefile (`build` → `dist/`,
  `build-all`, `package` with Developer ID signing + notarization, `brew`),
  `.gitignore`, MIT LICENSE, `config.example.toml`, and the release scripts.
- CLI shell: subcommand dispatch, `help`, and `version`. The `lookup`, `search`,
  `update`, `status` and `mcp` commands are recognised but not yet implemented.
- Package skeletons with their contracts fixed: `macaddr` (address parsing and
  classification, plus the well-known multicast table), `ouidb` (registry
  entries, assignment widths, the `Private` distinction, store record), `ieee`
  (registry catalogue, honest User-Agent), `config` (settings and TTL floor),
  `engine`, `mcp` (tool names).
- Zero external dependencies (standard library only).
