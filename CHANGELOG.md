# Changelog

Newest first. Before a final release, `scripts/release-notes.sh --changelog vX.Y.Z` adds its section from the Conventional Commits since the previous final tag; edit it and add upgrade notes under "Breaking changes" before tagging. The release workflow refuses a final tag without its section and uses it as the release notes. Release candidates are described on their GitHub releases only.
