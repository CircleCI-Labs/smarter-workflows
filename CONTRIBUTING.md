# Contributing

Thank you for helping to improve Smarter Workflows, a CircleCI Labs project. Field engineers review issues and pull requests on a best-effort basis, with no SLA. Report a security problem by email, not in an issue ([SECURITY.md](SECURITY.md)).

## Reporting a bug

Search the [existing issues](https://github.com/CircleCI-Labs/smarter-workflows/issues) first, and check that you use the latest release or `main`. Then open a new issue. Include:

- The planner version (`circleci-smarter version`, or the orb version) and the mode you ran in (`shadow` or `select`).
- The `plan.md` and `plan.json` that the plan job stores as the artifacts `smarter-plan/`. They list every job, whether it ran, and why.
- The files that the change touched, what the plan did, and what you expected.
- The part of your `continue.yml` that shows the problem, reduced to the few jobs that matter.

Remove tokens, internal host names and private code from everything you paste.

## Requesting a feature

Open an issue. Describe the problem, and name the command, the flag or the file it affects. For problems in the CircleCI platform, contact [CircleCI support](https://support.circleci.com/hc/en-us) instead.

## Opening a pull request

Before you open a pull request:

1. Discuss large changes in an issue first.
1. Run the checks of [Development](#development).
1. Update `docs/` when you change a flag, a file format or a default.
1. Write every commit message, and the pull request title, as a Conventional Commit ([Commit messages](#commit-messages)).
1. Check that the diff has no tokens, host names, or customer or vendor names. The repository is public.
1. Add a dependency only when its release is at least 7 days old, and pin it exactly: in `go.mod`, and by version and checksum for a tool that CI downloads.

A pull request that changes only documentation (`docs/`, `site/`, Markdown files) runs the docs site build and skips the jobs that do not read those files. That is the planner at work: this repository plans its own pipeline ([How it works](docs/how-it-works.md)).

### Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): summary`. Keep each commit small and atomic.

| Type | Use for |
|---|---|
| `feat` | A new capability, or a changed command, flag or file format |
| `fix` | A bug fix |
| `docs` | Documentation only |
| `test` | Tests only |
| `ci` | The CircleCI pipeline |
| `chore` | Maintenance with no user-visible change |
| `refactor`, `perf` | Code changes that keep behavior, or improve speed |

A breaking change adds `!` after the type or scope (`feat(plan)!: rename a flag`), or a `BREAKING CHANGE:` footer that says what the user has to change.

## Development

Requirements: Go (the version in `go.mod`), and the [CircleCI CLI](https://circleci.com/docs/guides/toolkit/circleci-cli/), which the tests use to validate every config that the planner generates. Config validation needs a CircleCI API token (`circleci auth login` locally, or the `smarter-workflows-ci` context with `CIRCLE_TOKEN` in CI on this project). Set `REQUIRE_CIRCLECI_CLI=1` to make a missing CLI or token fail the tests instead of skipping them.

```bash
gofmt -l .
go vet ./...
go test ./...
circleci policy test policies
```

The commands check formatting (the output must be empty), vet, the Go tests, and the Rego tests of the guardrail bundle. Restricted contexts in `policies/guardrails.rego` are `deploy`, `smarter-workflows-release`, and `smarter-workflows-ci` (API token for config validation on `go-test` and `orb-validate`). CI also runs `shellcheck -s sh scripts/*.sh`. The golden files of the example monorepo change with `go test ./examples/monorepo -update`: read the diff before you commit it.

[Validation](docs/validation.md) lists every test layer, and `.circleci/continue.yml` is the pipeline that runs them.

Keep the repository generic: no customer, vendor-specific or internal names, host names or links. Never print environment variables or secrets in a script, a test or a log.

## Documentation

Documentation follows the [CircleCI docs style](https://circleci.com/docs/contributors/docs-style/style-and-voice/): sentence-case headings, active voice, no contractions, and short sentences. Every `docs/*.md` page has front matter with `page-title`, `page-audience`, and `page-description`.

### Documentation site

The documentation is also a site on GitHub Pages: <https://circleci-labs.github.io/smarter-workflows/>. CircleCI builds it with [Hugo](https://gohugo.io/) and the [Hextra](https://github.com/imfing/hextra) theme, and pushes it to the `gh-pages` branch. The Markdown stays the only source, and nothing in it is written for the site: `README.md` is the home page, and `site/nav.txt` lists the other pages (`docs/`, the glossary, the composer skill, the compose eval, the demo, this file and the security policy).

**Add every new, renamed or removed page to `site/nav.txt`.** `site/stage.sh` fails on a Markdown file below `docs/` that the list misses, and on a listed file that does not exist, so `docs-site-build` catches a page that would be missing from the sidebar. `STAGE_LENIENT=1` turns both errors into warnings while you restructure. A relative link to a file that is a page of the site goes to that page, and a link to any other file of the repository goes to GitHub.

To preview the site, install Hugo extended 0.168.0 and Go (Hugo downloads the theme as a Go module), then run from the repository root:

```bash
site/stage.sh
cd site && hugo server --bind 127.0.0.1 --baseURL http://127.0.0.1:1313/
```

Run `site/stage.sh` again after you change a page. To run the check of the pipeline, install [lychee](https://github.com/lycheeverse/lychee) and run `(cd site && hugo --minify --panicOnWarning) && site/check.sh`. It checks every link and heading anchor of the built site, offline.

The job `docs-site-build` runs this check on every branch. It reads only the docs and the site sources, so the planner skips it for a change that touches neither. The job `docs-site-publish` runs on `main` only, in the workflow `docs-site`, and pushes the site to `gh-pages` with the context `smarter-workflows-release`. Hugo and lychee are pinned by version and SHA-256 in the command `docs-site` of `.circleci/continue.yml`: a new version needs its new checksum, from the release page of the tool, in the same change.

A maintainer does this once:

1. Put a fine-grained token for this repository only, with the repository permission **Contents: read and write** and nothing else, in the variable `GITHUB_TOKEN` of the context `smarter-workflows-release`. Without the variable the job builds and checks the site and publishes nothing.
1. Restrict the context to this project, and refuse SSH reruns:

   ```bash
   circleci context restriction create smarter-workflows-release --org <ORG> --type project --value <PROJECT_ID>
   circleci context restriction create smarter-workflows-release --org <ORG> --type expression \
     --value 'not job.ssh.enabled and not (pipeline.config_source starts-with "api")'
   ```

1. After the first publication, open Settings, Pages, and set **Source** to **Deploy from a branch**, the branch to `gh-pages` and the folder to `/ (root)`. GitHub then deploys every push to that branch. There is no workflow file to maintain.

## License

By contributing, you agree that your contributions are licensed under the [MIT license](LICENSE).
