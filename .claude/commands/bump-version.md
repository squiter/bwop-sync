Bump the bwop-sync version, update CHANGELOG.md, tag, and push.

Follow these steps exactly:

1. Run `git tag --sort=-version:refname | head -5` to find the latest tag.

2. Determine the new version:
   - If the user provided an argument (e.g. `/bump-version 0.2.0`), use that as the new version.
   - Otherwise ask: "Current version is X.Y.Z — bump major, minor, or patch?" and compute accordingly.
   - The version format is always `X.Y.Z` (no `v` prefix in the tag prompt, but the git tag uses `vX.Y.Z`).

3. Update CHANGELOG.md:
   - Replace `## [Unreleased]` with two blocks:
     ```
     ## [Unreleased]

     ## [X.Y.Z] - YYYY-MM-DD
     ```
     where YYYY-MM-DD is today's date.
   - Ask the user: "What changed in this release? (brief summary, or press Enter to leave Unreleased empty)"
     If they provide text, add it as bullet points under the new version heading.
   - Update the comparison links at the bottom:
     - Change the `[Unreleased]` link to compare `vX.Y.Z...HEAD`
     - Add a new `[X.Y.Z]` link pointing to the release tag

4. Stage and commit only CHANGELOG.md:
   ```
   git add CHANGELOG.md
   git commit -m "Prepare release vX.Y.Z"
   ```

5. Create an annotated tag:
   ```
   git tag -a vX.Y.Z -m "Release vX.Y.Z"
   ```

6. Push commit and tag:
   ```
   git push
   git push origin vX.Y.Z
   ```

7. Print the GitHub Actions URL so the user can watch the release build:
   `https://github.com/squiter/bwop-sync/actions`
