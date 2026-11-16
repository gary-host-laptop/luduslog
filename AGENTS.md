# Agent Guidelines

## Git etiquette

### Issues

- Assign every issue to an owner before starting work. Unless told otherwise,
  assign it to the maintainer, [@gary-host-laptop](https://github.com/gary-host-laptop).
- Reference related issues in discussion rather than creating duplicate issues.

### Commits

- Reference the issue number in the commit message when one applies, for example
  `Fix review byline alignment (#24)`.
- Keep commits focused on the issue or task they describe.
- Do not force-push or rewrite published history.

### Closing issues

- Do not close an issue until its change is committed and successfully pushed
  to the remote.
- After a successful push, comment on the issue with the commit SHA and a short
  summary of what landed.
- Only then close the issue.
- If the push fails, leave the issue open and report the failure.
