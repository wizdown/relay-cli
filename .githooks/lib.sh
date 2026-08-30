# Credential shapes shared by the hooks in this directory. Sourced, not run.
#
# One home for the pattern. pre-commit scans the diff, commit-msg scans the
# message, and a placeholder that is legal in one has to be legal in the other —
# two copies of this list is two copies that drift.

# A relay connector secret. The whole credential is the URL; this is the part of
# it that is unmistakable.
CONNECTOR_RE='wzh_[A-Za-z0-9_-]{6,}'

# Values allowed because they are obviously not real: the placeholder the docs
# and `relay init` use, and the fixtures the tests assert on. Matched as a
# PREFIX, so `wzh_REPLACE_ME_2` in docs/configuration.md is covered too.
PLACEHOLDER_RE='^wzh_(REPLACE_ME|REDACTED|secret_?value|supersecretvalue|longsecrettoken|someotherunknownsecret|abcdefghijkl|a{6,}|b{6,}|abc)'
