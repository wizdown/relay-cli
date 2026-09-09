# Credential shapes shared by everything in this repo that scans for a secret.
# Sourced, not run.
#
# One home for the pattern. pre-commit scans the diff, commit-msg scans the
# message, scripts/scan-secrets.sh scans files or stdin for the hooks, for CI
# and for the pull-request text — and a placeholder that is legal in one has to
# be legal in all of them. Two copies of this list is two copies that drift.

# A relay connector secret. The whole credential is the URL; this is the part of
# it that is unmistakable.
CONNECTOR_RE='wzh_[A-Za-z0-9_-]{6,}'

# Values allowed because they are obviously not real: the placeholder the docs
# and `relay init` use, and the fixtures the tests assert on. Matched as a
# PREFIX, so `wzh_REPLACE_ME_2` in docs/configuration.md is covered too.
PLACEHOLDER_RE='^wzh_(REPLACE_ME|REDACTED|secret_?value|supersecretvalue|longsecrettoken|someotherunknownsecret|realsecret|abcdefghijkl|a{6,}|b{6,}|abc)'

# The two lines a caller prints after a hit. They live here so the placeholder
# is named in one file: every other scanner can then be grepped for the
# credential shape and come back clean.
PLACEHOLDER_HINT='Use relay.example.com and wzh_REPLACE_ME in committed files.'
REVOKE_HINT='If this is a real credential, revoke it in relay before anything else.'
